// Package ui - app.go is the root Bubbletea model for Wrangler.
package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jonhyblaze/wrangler/internal/media"
	"github.com/jonhyblaze/wrangler/internal/meta"
	"github.com/jonhyblaze/wrangler/internal/report"
	"github.com/jonhyblaze/wrangler/internal/transaction"
)

// AppModel is the root Bubbletea model.
type AppModel struct {
	width  int
	height int

	activePanel Panel
	browser     BrowserModel
	detail      DetailModel
	queue       QueueModel

	transactions   []*transaction.Transaction
	focusedTxIndex int // index into transactions shown in detail panel

	activeRunner   *transaction.Runner
	activeVerifier *transaction.Verifier

	// Pause state: exactly one runner can be paused at a time (via SIGSTOP).
	// pausedRunner / pausedTx are set on every pause — both manual [p] and
	// priority-start [s].  priorityTxID is only set for priority-start; when
	// it is non-empty the paused transfer auto-resumes after the active one
	// finishes.  For a manual pause priorityTxID stays "".
	//
	// INVARIANT: activeRunner points only to a *running* runner (never to a
	// SIGSTOP-ed one). After any pause, activeRunner is cleared and the runner
	// is moved here.
	pausedRunner *transaction.Runner
	pausedTx     *transaction.Transaction
	priorityTxID string

	watcher *media.Watcher
	keys    KeyMap

	// Eject state.
	confirmingEject bool
	forceEject      bool

	// Status message (shown in header for a moment).
	statusMsg string

	// Rsync unavailable warning.
	rsyncWarning string

	// Info popup overlay.
	showInfoPopup bool
	infoPopup     InfoPopupModel
}

// NewApp creates the root AppModel.
func NewApp(watcher *media.Watcher) AppModel {
	keys := DefaultKeyMap()
	vols := watcher.CurrentVolumes()

	m := AppModel{
		activePanel:    PanelBrowser,
		browser:        NewBrowser(vols),
		detail:         NewDetail(),
		queue:          NewQueue(),
		watcher:        watcher,
		keys:           keys,
		focusedTxIndex: -1,
	}

	return m
}

// Init implements tea.Model.
func (m AppModel) Init() tea.Cmd {
	return tea.Batch(
		m.watcher.NextVolumeMsg(),
	)
}

// Update implements tea.Model.
func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizePanels()
		return m, nil

	case media.VolumesChangedMsg:
		m.browser.SetVolumes(msg.Volumes)
		return m, m.watcher.NextVolumeMsg()

	case transaction.ProgressMsg:
		// Update the transaction in our list.
		for _, tx := range m.transactions {
			if tx.ID == msg.TxID {
				tx.UpdateProgress(msg.Progress)
				break
			}
		}
		// Only restart the polling loop for the currently-active runner.
		// Stale NextProgressMsg goroutines from a previously-active runner
		// (e.g. after a priority-swap) must not spawn new loops for the new
		// active runner — that would cause both transactions to appear active.
		if m.activeRunner != nil && m.activeRunner.TxID() == msg.TxID {
			return m, m.activeRunner.NextProgressMsg()
		}
		return m, nil

	case transaction.RunnerDoneMsg:
		m.activeRunner = nil
		if msg.Err != nil {
			// Error already recorded in transaction.
			// Resume a paused runner (priority or manual) if one is waiting.
			if (m.priorityTxID != "" && msg.TxID == m.priorityTxID) || m.pausedRunner != nil {
				return m, m.tryResumePaused()
			}
			return m, m.advanceQueue()
		}
		// Find the transaction and start verification.
		for _, tx := range m.transactions {
			if tx.ID == msg.TxID {
				verifier := transaction.NewVerifier(tx)
				m.activeVerifier = verifier
				verifier.Start(context.Background())
				return m, verifier.NextVerifyMsg()
			}
		}
		// Transaction not found (shouldn't happen).
		if (m.priorityTxID != "" && msg.TxID == m.priorityTxID) || m.pausedRunner != nil {
			return m, m.tryResumePaused()
		}
		return m, m.advanceQueue()

	case transaction.VerifyProgressMsg:
		// Update verify progress on the transaction.
		for _, tx := range m.transactions {
			if tx.ID == msg.TxID {
				snap := tx.Snapshot()
				vr := snap.Verify
				vr.FilesChecked = msg.Checked
				vr.FilesTotal = msg.Total
				tx.SetVerifyResult(vr)
				break
			}
		}
		// Guard: only restart the loop for the currently-active verifier.
		// Stale heartbeats from a previous verifier must not re-spawn loops.
		if m.activeVerifier != nil && m.activeVerifier.TxID() == msg.TxID {
			return m, m.activeVerifier.NextVerifyMsg()
		}
		return m, nil

	case transaction.VerifyDoneMsg:
		m.activeVerifier = nil
		// Write report for the completed transaction.
		for _, tx := range m.transactions {
			if tx.ID == msg.TxID {
				snap := tx.Snapshot()
				paths, err := report.Write(snap)
				if err == nil {
					tx.SetReportPaths(paths)
				}
				break
			}
		}
		// If there is a paused runner waiting (priority-paused or manually-paused
		// while this tx ran), resume it now. For priority-start, priorityTxID
		// matches; for manual pause, priorityTxID is "" but pausedRunner != nil.
		if (m.priorityTxID != "" && msg.TxID == m.priorityTxID) || m.pausedRunner != nil {
			return m, m.tryResumePaused()
		}
		// Advance queue to run next queued transaction.
		return m, m.advanceQueue()

	case media.EjectMsg:
		m.confirmingEject = false
		m.browser.confirmEject = false
		m.browser.busyProcess = ""
		r := msg.Result
		switch {
		case r.Success && r.AutoForced:
			// System process was blocking — we force-ejected silently.
			m.statusMsg = GreenStyle.Render("✓ ") + fmt.Sprintf("Force-ejected %s (system process released)", r.MountPoint)
		case r.Success:
			m.statusMsg = GreenStyle.Render("✓ ") + fmt.Sprintf("Ejected %s", r.MountPoint)
		case r.Err == media.ErrBusy && r.BusyProcess != "":
			// A specific user app is holding the volume — ask to force.
			m.statusMsg = AmberStyle.Render("⚠ ") + fmt.Sprintf(
				"%s is using this volume — [f] to force eject", r.BusyProcess)
			// Re-enter confirmation state so [f] can force.
			m.confirmingEject = true
			m.forceEject = true
			m.browser.confirmEject = true
			m.browser.ejectTarget = r.MountPoint
			m.browser.busyProcess = r.BusyProcess
		case r.Err == media.ErrBusy:
			m.statusMsg = AmberStyle.Render("⚠ ") + "Volume busy — [f] to force eject"
			m.confirmingEject = true
			m.forceEject = true
			m.browser.confirmEject = true
			m.browser.ejectTarget = r.MountPoint
			m.browser.busyProcess = ""
		default:
			m.statusMsg = RedStyle.Render("✗ ") + fmt.Sprintf("Eject failed: %v", r.Err)
		}
		return m, m.watcher.NextVolumeMsg()

	case FocusTransactionMsg:
		if msg.Index >= 0 && msg.Index < len(m.transactions) {
			m.focusedTxIndex = msg.Index
			m.detail.SetTransaction(m.transactions[msg.Index])
		}
		return m, nil

	// Info popup messages.
	case meta.LoadedMsg:
		if m.showInfoPopup && msg.Path == m.infoPopup.path {
			m.infoPopup.info = msg.M
			m.infoPopup.loading = false
		}
		return m, nil

	case meta.SpinTickMsg:
		if m.showInfoPopup && m.infoPopup.loading && msg.Path == m.infoPopup.path {
			m.infoPopup.spinFrame++
			return m, SpinCmd(msg.Path)
		}
		return m, nil

	case InfoCopiedMsg:
		m.infoPopup.copiedKey = msg.Key
		return m, copyClearCmd()

	case InfoCopiedClearMsg:
		m.infoPopup.copiedKey = ""
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// handleKey processes key events.
func (m AppModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// When the info popup is open, route all keys there.
	if m.showInfoPopup {
		return m.handleInfoPopupKey(msg)
	}

	// Global keys.
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.keys.Tab):
		m.activePanel = (m.activePanel + 1) % 3
		return m, nil

	case key.Matches(msg, m.keys.NewTransaction):
		// [n] switches focus to the browser to start a new transaction.
		m.activePanel = PanelBrowser
		if m.browser.selectingDest {
			m.browser.Reset() // cancel any in-progress selection
		}
		return m, nil
	}

	// Panel-specific key handling.
	switch m.activePanel {
	case PanelBrowser:
		return m.handleBrowserKey(msg)
	case PanelDetail:
		return m.handleDetailKey(msg)
	case PanelQueue:
		return m.handleQueueKey(msg)
	}
	return m, nil
}

// handleBrowserKey processes key events for the browser panel.
func (m AppModel) handleBrowserKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Eject confirmation takes priority.
	if m.browser.confirmEject {
		switch {
		case key.Matches(msg, m.keys.Confirm):
			target := m.browser.ConfirmEject()
			m.confirmingEject = false
			if target != "" {
				return m, media.EjectCmd(target, false)
			}
		case key.Matches(msg, m.keys.ForceEject):
			target := m.browser.ConfirmEject()
			m.confirmingEject = false
			if target != "" {
				return m, media.EjectCmd(target, true)
			}
		case key.Matches(msg, m.keys.Back):
			m.browser.ConfirmEject()
			m.browser.busyProcess = ""
			m.confirmingEject = false
			m.statusMsg = ""
		}
		return m, nil
	}

	switch {
	case key.Matches(msg, m.keys.Up):
		m.browser.MoveUp()

	case key.Matches(msg, m.keys.Down):
		m.browser.MoveDown()

	case key.Matches(msg, m.keys.PageUp):
		m.browser.PageUp()

	case key.Matches(msg, m.keys.PageDown):
		m.browser.PageDown()

	case key.Matches(msg, m.keys.NavigateInto):
		m.browser.NavigateInto()

	case key.Matches(msg, m.keys.NavigateUp):
		m.browser.NavigateUp()

	case key.Matches(msg, m.keys.GotoHome):
		m.browser.GotoHome()

	case key.Matches(msg, m.keys.GotoVolumes):
		if !m.browser.selectingDest {
			m.browser.GotoVolumes()
		}

	case key.Matches(msg, m.keys.Back):
		if m.browser.selectingDest {
			m.browser.Reset()
		} else {
			m.browser.NavigateUp()
		}

	case key.Matches(msg, m.keys.Eject):
		if !m.browser.selectingDest {
			target := m.browser.StartEject()
			if target != "" {
				m.confirmingEject = true
				m.forceEject = false
			}
		}

	case key.Matches(msg, m.keys.Info):
		e, ok := m.browser.current()
		if ok && e.path != "" && !e.isHeader && !e.isSeparator {
			var cmd tea.Cmd
			m.infoPopup, cmd = NewInfoPopup(e.path)
			m.showInfoPopup = true
			return m, cmd
		}

	case key.Matches(msg, m.keys.Select):
		// Space = select highlighted item as source then destination.
		src, dests, ready := m.browser.Select()
		if ready {
			return m, m.createTransaction(src, dests)
		}
		_ = src // partial selection in progress
	}

	return m, nil
}

// handleInfoPopupKey handles key events when the info popup is visible.
func (m AppModel) handleInfoPopupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "i":
		m.showInfoPopup = false
		return m, nil

	case "s":
		// Set highlighted path as source and continue to destination selection.
		if m.infoPopup.info != nil && !m.infoPopup.info.NotFound {
			m.browser.selectedSource = m.infoPopup.info.Path
			m.browser.selectingDest = true
		}
		m.showInfoPopup = false
		return m, nil

	case "d":
		// Add path as destination (only valid while choosing a destination).
		if m.infoPopup.info != nil && !m.infoPopup.info.NotFound && m.browser.selectingDest {
			p := m.infoPopup.info.Path
			// Deduplicate.
			dup := false
			for _, d := range m.browser.selectedDests {
				if d == p {
					dup = true
					break
				}
			}
			if !dup && p != m.browser.selectedSource {
				m.browser.selectedDests = append(m.browser.selectedDests, p)
			}
		}
		m.showInfoPopup = false
		return m, nil

	case "c":
		if m.infoPopup.info != nil && !m.infoPopup.info.NotFound {
			return m, copyCmd("path", m.infoPopup.info.Path)
		}

	case "b":
		if m.infoPopup.info != nil && !m.infoPopup.info.NotFound {
			bc := meta.Breadcrumb(m.infoPopup.info.Path, 500)
			return m, copyCmd("breadcrumb", bc)
		}

	case "m":
		if m.infoPopup.info != nil && !m.infoPopup.info.NotFound {
			return m, copyCmd("meta", metaAsText(m.infoPopup.info))
		}
	}
	return m, nil
}

// createTransaction adds a new transaction and starts it if the queue is idle.
func (m *AppModel) createTransaction(src string, dests []string) tea.Cmd {
	tx := transaction.New(src, dests)
	m.transactions = append(m.transactions, tx)
	m.queue.SetTransactions(m.transactions)
	newIdx := len(m.transactions) - 1
	m.focusedTxIndex = newIdx
	m.detail.SetTransaction(m.transactions[newIdx])
	m.queue.focusedIndex = newIdx
	m.queue.cursor = newIdx
	return m.advanceQueue()
}

// handleDetailKey processes key events for the detail panel.
func (m AppModel) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.focusedTxIndex < 0 || m.focusedTxIndex >= len(m.transactions) {
		return m, nil
	}
	tx := m.transactions[m.focusedTxIndex]
	state := tx.GetState()

	switch {
	case key.Matches(msg, m.keys.Pause):
		switch state {
		case transaction.StateRunning:
			if m.activeRunner != nil {
				if err := m.activeRunner.Pause(); err == nil {
					_ = tx.SetState(transaction.StatePaused)
					// Move runner out of activeRunner so advanceQueue / startNow
					// don't see a running runner.  priorityTxID stays "" (manual
					// pause — no auto-resume; user must press [p] explicitly).
					m.pausedRunner = m.activeRunner
					m.pausedTx = tx
					m.activeRunner = nil
				}
			}
		case transaction.StatePaused:
			// If there is an active runner to swap with, do the swap.
			if m.pausedTx == tx && m.activeRunner != nil {
				return m.swapPaused()
			}
			// Direct resume: either a manual pause, or priority tx already
			// finished but tryResumePaused somehow didn't fire (fallback).
			if m.pausedTx == tx && m.pausedRunner != nil {
				if err := m.pausedRunner.Resume(); err == nil {
					_ = tx.SetState(transaction.StateRunning)
					m.activeRunner = m.pausedRunner
					m.pausedRunner = nil
					m.pausedTx = nil
					// Clear any leftover priority state.
					m.priorityTxID = ""
					m.queue.SetPriorityPausedID("")
					m.detail.SetPriorityPausedID("")
					return m, m.activeRunner.NextProgressMsg()
				}
			}
		}

	case key.Matches(msg, m.keys.Cancel):
		switch state {
		case transaction.StateRunning:
			if m.activeRunner != nil {
				m.activeRunner.Cancel()
				m.activeRunner = nil
				// If this is the priority tx, RunnerDoneMsg will call
				// tryResumePaused. Don't touch the paused runner here.
				if m.priorityTxID != "" && tx.ID == m.priorityTxID {
					return m, nil
				}
				// Non-priority active runner cancelled — also discard any paused
				// runner so nothing resumes unexpectedly.
				if m.pausedRunner != nil {
					m.pausedRunner.Cancel()
					m.pausedRunner = nil
					m.pausedTx = nil
					m.priorityTxID = ""
					m.queue.SetPriorityPausedID("")
					m.detail.SetPriorityPausedID("")
				}
				return m, m.advanceQueue()
			}

		case transaction.StatePaused:
			// Cancel the paused runner (both priority-paused and manually-paused
			// land here — they both use m.pausedRunner now).
			if m.pausedTx == tx && m.pausedRunner != nil {
				m.pausedRunner.Cancel()
				// Cancel() calls SetStateForce(Cancelled) internally.
				m.pausedRunner = nil
				m.pausedTx = nil
				m.queue.SetPriorityPausedID("")
				m.detail.SetPriorityPausedID("")
				// If a priority tx is still running, keep priorityTxID — when it
				// finishes tryResumePaused will see pausedRunner==nil and advance.
				// If nothing is running, clear priorityTxID too.
				if m.activeRunner == nil {
					m.priorityTxID = ""
				}
				return m, nil
			}

		case transaction.StateQueued:
			_ = tx.SetState(transaction.StateCancelled)
		}

	case key.Matches(msg, m.keys.StartPriority):
		if state == transaction.StateQueued {
			return m.startNow(tx)
		}

	case key.Matches(msg, m.keys.OpenReport):
		if state == transaction.StateDone {
			snap := tx.Snapshot()
			if len(snap.Report.Paths) > 0 {
				openFile(snap.Report.Paths[0])
			}
		}

	case key.Matches(msg, m.keys.Up):
		if m.focusedTxIndex > 0 {
			m.focusedTxIndex--
			m.detail.SetTransaction(m.transactions[m.focusedTxIndex])
			m.queue.focusedIndex = m.focusedTxIndex
		}

	case key.Matches(msg, m.keys.Down):
		if m.focusedTxIndex < len(m.transactions)-1 {
			m.focusedTxIndex++
			m.detail.SetTransaction(m.transactions[m.focusedTxIndex])
			m.queue.focusedIndex = m.focusedTxIndex
		}
	}

	return m, nil
}

// handleQueueKey processes key events for the queue panel.
func (m AppModel) handleQueueKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Up):
		m.queue.MoveUp()

	case key.Matches(msg, m.keys.Down):
		m.queue.MoveDown()

	// Both Enter and Space focus the highlighted transaction in the detail panel.
	case key.Matches(msg, m.keys.NavigateInto), key.Matches(msg, m.keys.Select):
		focusMsg := m.queue.SelectCurrent()
		if focusMsg.Index >= 0 && focusMsg.Index < len(m.transactions) {
			m.focusedTxIndex = focusMsg.Index
			m.detail.SetTransaction(m.transactions[focusMsg.Index])
		}

	// [s] force-starts the highlighted queued transaction immediately.
	case key.Matches(msg, m.keys.StartPriority):
		return m.handleQueueStart()

	// [p] pause / resume the highlighted transaction.
	case key.Matches(msg, m.keys.Pause):
		return m.handleQueuePause()

	// [c] cancel the highlighted transaction.
	case key.Matches(msg, m.keys.Cancel):
		return m.handleQueueCancel()
	}

	return m, nil
}

// swapPaused swaps the currently-running priority transaction with the
// priority-paused one. Called when the user presses [p] on the paused-for-
// priority transaction to toggle back to it. Suspends the current runner via
// SIGSTOP and resumes the paused runner via SIGCONT, updating all bookkeeping.
func (m AppModel) swapPaused() (tea.Model, tea.Cmd) {
	if m.pausedRunner == nil || m.pausedTx == nil || m.activeRunner == nil {
		return m, nil
	}

	// Identify the currently running transaction (the one we want to suspend).
	var activeTx *transaction.Transaction
	for _, t := range m.transactions {
		if t.GetState() == transaction.StateRunning {
			activeTx = t
			break
		}
	}
	if activeTx == nil {
		return m, nil
	}

	// Suspend the currently active runner.
	if err := m.activeRunner.Pause(); err != nil {
		// Pause failed (pgid race) — don't corrupt state.
		return m, nil
	}
	_ = activeTx.SetState(transaction.StatePaused)

	// Resume the previously-paused runner.
	if err := m.pausedRunner.Resume(); err != nil {
		// Could not resume — undo the suspend.
		_ = m.activeRunner.Resume()
		_ = activeTx.SetState(transaction.StateRunning)
		return m, nil
	}
	_ = m.pausedTx.SetState(transaction.StateRunning)

	// Swap: the old paused tx is now the active one; the old active tx is paused.
	prevPausedRunner := m.pausedRunner
	prevPausedTx := m.pausedTx
	m.pausedRunner = m.activeRunner
	m.pausedTx = activeTx
	m.activeRunner = prevPausedRunner
	// The "priority" is now whichever transaction is currently running — when it
	// finishes, the paused one will be auto-resumed by tryResumePaused.
	m.priorityTxID = prevPausedTx.ID
	m.queue.SetPriorityPausedID(activeTx.ID)
	m.detail.SetPriorityPausedID(activeTx.ID)

	return m, m.activeRunner.NextProgressMsg()
}

// handleQueuePause toggles pause/resume on the queue-panel cursor transaction.
func (m AppModel) handleQueuePause() (tea.Model, tea.Cmd) {
	idx := m.queue.cursor
	if idx < 0 || idx >= len(m.transactions) {
		return m, nil
	}
	tx := m.transactions[idx]

	switch tx.GetState() {
	case transaction.StateRunning:
		if m.activeRunner != nil {
			if err := m.activeRunner.Pause(); err == nil {
				_ = tx.SetState(transaction.StatePaused)
				m.pausedRunner = m.activeRunner
				m.pausedTx = tx
				m.activeRunner = nil
			}
		}
	case transaction.StatePaused:
		// If there is an active runner to swap with, do the swap.
		if m.pausedTx == tx && m.activeRunner != nil {
			return m.swapPaused()
		}
		// Direct resume (manual pause, or fallback when priority tx already done).
		if m.pausedTx == tx && m.pausedRunner != nil {
			if err := m.pausedRunner.Resume(); err == nil {
				_ = tx.SetState(transaction.StateRunning)
				m.activeRunner = m.pausedRunner
				m.pausedRunner = nil
				m.pausedTx = nil
				m.priorityTxID = ""
				m.queue.SetPriorityPausedID("")
				m.detail.SetPriorityPausedID("")
				return m, m.activeRunner.NextProgressMsg()
			}
		}
	}
	return m, nil
}

// handleQueueCancel cancels the queue-panel cursor transaction.
func (m AppModel) handleQueueCancel() (tea.Model, tea.Cmd) {
	idx := m.queue.cursor
	if idx < 0 || idx >= len(m.transactions) {
		return m, nil
	}
	tx := m.transactions[idx]

	switch tx.GetState() {
	case transaction.StateRunning:
		if m.activeRunner != nil {
			m.activeRunner.Cancel()
			m.activeRunner = nil
			if m.priorityTxID != "" && tx.ID == m.priorityTxID {
				// Priority tx cancelled — RunnerDoneMsg will call tryResumePaused.
				return m, nil
			}
			if m.pausedRunner != nil {
				m.pausedRunner.Cancel()
				m.pausedRunner = nil
				m.pausedTx = nil
				m.priorityTxID = ""
				m.queue.SetPriorityPausedID("")
				m.detail.SetPriorityPausedID("")
			}
			return m, m.advanceQueue()
		}

	case transaction.StatePaused:
		// Cancel the paused runner (priority-paused and manually-paused both
		// live in m.pausedRunner now).
		if m.pausedTx == tx && m.pausedRunner != nil {
			m.pausedRunner.Cancel()
			m.pausedRunner = nil
			m.pausedTx = nil
			m.queue.SetPriorityPausedID("")
			m.detail.SetPriorityPausedID("")
			if m.activeRunner == nil {
				m.priorityTxID = ""
			}
			return m, nil
		}

	case transaction.StateQueued:
		_ = tx.SetState(transaction.StateCancelled)
	}
	return m, nil
}

// handleQueueStart handles [s] in the queue panel — delegates to startNow.
func (m AppModel) handleQueueStart() (tea.Model, tea.Cmd) {
	idx := m.queue.cursor
	if idx < 0 || idx >= len(m.transactions) {
		return m, nil
	}
	return m.startNow(m.transactions[idx])
}

// startNow immediately starts tx, pausing any active transfer so it can be
// auto-resumed once tx finishes. Safe to call from queue or detail panel.
func (m AppModel) startNow(tx *transaction.Transaction) (tea.Model, tea.Cmd) {
	if tx.GetState() != transaction.StateQueued {
		return m, nil // only Queued transactions can be force-started
	}

	// Do not interrupt an in-progress verification pass (it is fast and must
	// finish for data integrity reasons).
	if m.activeVerifier != nil {
		return m, nil
	}

	// Do not allow nesting — one paused runner at a time.
	if m.pausedRunner != nil {
		return m, nil
	}

	// Pause the currently active transfer so we can resume it later.
	if m.activeRunner != nil {
		// Find the transaction that is actually Running (rsync active).
		var runningTx *transaction.Transaction
		for _, t := range m.transactions {
			if t.GetState() == transaction.StateRunning {
				runningTx = t
				break
			}
		}
		if runningTx == nil {
			// The active runner is still in its initialisation phase (calculateSize /
			// checkSpace) — rsync has not started, so we cannot send SIGSTOP yet.
			// Return nil to avoid creating a competing goroutine for the same tx.
			return m, nil
		}
		// Attempt to suspend rsync via SIGSTOP.
		if err := m.activeRunner.Pause(); err != nil {
			// Pause failed (e.g. pgid not yet set in a narrow race).
			// Do NOT orphan the existing runner — bail out safely instead.
			return m, nil
		}
		_ = runningTx.SetState(transaction.StatePaused)
		m.pausedRunner = m.activeRunner
		m.pausedTx = runningTx
		// Mark the paused transaction in queue and detail views (↩ badge).
		m.queue.SetPriorityPausedID(runningTx.ID)
		m.detail.SetPriorityPausedID(runningTx.ID)
		m.activeRunner = nil
	}

	// Start the selected transaction immediately.
	runner := transaction.NewRunner(tx)
	m.activeRunner = runner
	m.priorityTxID = tx.ID
	runner.Start()
	return m, runner.NextProgressMsg()
}

// tryResumePaused is called when a priority transaction finishes (any outcome).
// It resumes the transfer that was paused to make room, or advances the queue
// normally if the paused transfer was cancelled in the meantime.
func (m *AppModel) tryResumePaused() tea.Cmd {
	m.priorityTxID = ""
	m.queue.SetPriorityPausedID("")  // clear the ↩ badge
	m.detail.SetPriorityPausedID("") // clear detail annotation

	runner := m.pausedRunner
	tx := m.pausedTx
	m.pausedRunner = nil
	m.pausedTx = nil

	if runner == nil || tx == nil {
		return m.advanceQueue()
	}

	// The paused transaction may have been cancelled by the user while the
	// active transfer was running — respect that and advance the queue instead.
	s := tx.GetState()
	if s == transaction.StateCancelled || s == transaction.StateFailed {
		return m.advanceQueue()
	}

	// Resume the rsync process (SIGCONT).
	if err := runner.Resume(); err != nil {
		tx.SetError(fmt.Errorf("could not resume paused transfer: %w", err))
		return m.advanceQueue()
	}

	// Transition the transaction back to Running.
	if err := tx.SetState(transaction.StateRunning); err != nil {
		// State machine rejected the transition (shouldn't happen, but be safe).
		return m.advanceQueue()
	}

	m.activeRunner = runner
	return runner.NextProgressMsg()
}

// advanceQueue starts the next QUEUED transaction if nothing is active or paused.
// A paused runner means the user deliberately suspended a transfer; don't start
// more work behind their back — they must resume or cancel it first.
func (m *AppModel) advanceQueue() tea.Cmd {
	if m.activeRunner != nil || m.pausedRunner != nil {
		return nil
	}
	for _, tx := range m.transactions {
		if tx.GetState() == transaction.StateQueued {
			runner := transaction.NewRunner(tx)
			m.activeRunner = runner
			runner.Start()
			return runner.NextProgressMsg()
		}
	}
	return nil
}

// View implements tea.Model.
func (m AppModel) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	// Info popup overlays the entire screen.
	if m.showInfoPopup {
		popup := m.infoPopup.View()
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, popup)
	}

	header := m.renderHeader()
	footer := m.renderFooter()

	headerH := lipgloss.Height(header)
	footerH := lipgloss.Height(footer)
	contentH := m.height - headerH - footerH
	if contentH < 1 {
		contentH = 1
	}

	// Panel widths.
	bw, dw, qw := m.panelWidths()

	// Set heights on sub-models.
	// contentH - 2: the Lipgloss panel Height is the inner content area; the
	// two border rows are added on top, so sub-models must not exceed contentH-2.
	innerH := contentH - 2
	if innerH < 1 {
		innerH = 1
	}

	bm := m.browser
	bm.width = bw
	bm.height = innerH

	dm := m.detail
	dm.width = dw
	dm.height = innerH

	qm := m.queue
	qm.width = qw
	qm.height = innerH

	// Render panels with borders.
	// In Lipgloss v1.x Width/Height are the total outer dimensions (border +
	// padding + content). Pass the full column widths so the three panels span
	// exactly m.width together, matching the header and footer.
	var browserStyle, detailStyle, queueStyle lipgloss.Style
	if m.activePanel == PanelBrowser {
		browserStyle = ActivePanelStyle
	} else {
		browserStyle = PanelStyle
	}
	if m.activePanel == PanelDetail {
		detailStyle = ActivePanelStyle
	} else {
		detailStyle = PanelStyle
	}
	if m.activePanel == PanelQueue {
		queueStyle = ActivePanelStyle
	} else {
		queueStyle = PanelStyle
	}

	// Lipgloss adds the border (2 cols / 2 rows) *outside* the width/height, but
	// counts Padding *inside* them. So to make a panel's OUTER size exactly fill
	// its allocated column `xw` and the content row height `contentH`:
	//   • Width(xw - 2):  outer = (xw-2) + 2 border = xw. The three panels then
	//     sum to exactly m.width, matching the full-width header and footer.
	//   • Height(contentH - 2): outer = (contentH-2) + 2 border = contentH.
	// The usable text area is xw - PanelFrameWidth (the extra 2 is Padding), which
	// is what each sub-model draws to (see m.width - PanelFrameWidth in their View).
	browserPanel := browserStyle.
		Width(bw - 2).
		Height(contentH - 2).
		Render(bm.View())

	detailPanel := detailStyle.
		Width(dw - 2).
		Height(contentH - 2).
		Render(dm.View())

	queuePanel := queueStyle.
		Width(qw - 2).
		Height(contentH - 2).
		Render(qm.View())

	content := lipgloss.JoinHorizontal(lipgloss.Top, browserPanel, detailPanel, queuePanel)

	return lipgloss.JoinVertical(lipgloss.Left, header, content, footer)
}

// renderHeader renders the top header bar.
//
// Fixed two-row layout — height never changes regardless of state:
//
//	Row 1: DATA WRANGLER  ████████████░░░░░░░░  72%
//	Row 2: status / log line (empty when idle)
func (m AppModel) renderHeader() string {
	bg := ColorSurface

	// Inner content width (HeaderStyle has Padding(0,1) → 1 char each side).
	innerW := m.width - 2
	if innerW < 4 {
		innerW = 4
	}

	// ── Row 1: title + progress bar ─────────────────────────────────────────
	title := lipgloss.NewStyle().
		Foreground(ColorAmber).Bold(true).Background(bg).
		Render("DATA WRANGLER")

	titleW := lipgloss.Width(title)

	// Look for an active / verifying transaction to show a progress bar.
	var activePct float64
	hasActive := false
	for _, tx := range m.transactions {
		snap := tx.Snapshot()
		if snap.State == transaction.StateRunning || snap.State == transaction.StateVerifying {
			activePct = snap.Progress.Percent()
			hasActive = true
			break
		}
	}

	pctLabel := ""
	pctW := 0
	barW := 0
	if hasActive {
		pctLabel = fmt.Sprintf("%3.0f%%", activePct*100)
		pctW = len(pctLabel) + 1 // 1 leading space
		// Bar fills the space between title and percentage label.
		// 2 spaces padding on each side of the bar.
		barW = innerW - titleW - pctW - 4
		if barW < 1 {
			barW = 1
		}
	}

	var row1 string
	if hasActive {
		bar := lipgloss.NewStyle().Foreground(ColorAmber).Background(bg).
			Render(humanize_bar(activePct, barW))
		pct := lipgloss.NewStyle().Foreground(ColorMuted).Background(bg).
			Render(" " + pctLabel)
		pad := lipgloss.NewStyle().Background(bg).Render("  ")
		// Fill remaining space so the row background is solid.
		gap := innerW - titleW - barW - lipgloss.Width(pct) - 4
		if gap < 0 {
			gap = 0
		}
		filler := lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", gap))
		row1 = title + pad + bar + pad + pct + filler
	} else {
		// No active transaction — title only, rest filled with bg.
		rest := innerW - titleW
		if rest < 0 {
			rest = 0
		}
		row1 = title + lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", rest))
	}

	// ── Row 2: status / log line (always exactly one row) ───────────────────
	var statusContent string
	if m.statusMsg != "" {
		// statusMsg may already contain ANSI colour codes — render as-is but
		// ensure the full row has the surface background.
		statusContent = m.statusMsg
	}
	row2 := lipgloss.NewStyle().
		Background(bg).Width(innerW).
		Render(statusContent)

	return HeaderStyle.Width(m.width).Render(row1 + "\n" + row2)
}

// renderFooter renders the bottom keybinding footer.
func (m AppModel) renderFooter() string {
	var parts []string
	// add renders one "[key] description" hint. Both halves carry the surface
	// background so the whole footer stays one solid bar (see FooterKeyStyle).
	add := func(key, desc string) {
		parts = append(parts, FooterKeyStyle.Render(key)+FooterDescStyle.Render(" "+desc))
	}

	add("[tab]", "panels")

	switch m.activePanel {
	case PanelBrowser:
		if m.browser.confirmEject {
			add("[y]", "confirm eject")
			add("[f]", "force")
			add("[esc]", "cancel")
		} else if m.browser.selectingDest {
			add("[space]", "set dest")
			add("[→/enter]", "open dir")
			add("[←/bksp]", "parent")
			add("[esc]", "cancel")
		} else {
			add("[space]", "set source")
			add("[→/enter]", "open dir")
			add("[←/bksp]", "parent")
			add("[i]", "info")
			add("[~]", "home")
			add("[v]", "/Volumes")
			add("[e]", "eject")
		}
	case PanelDetail:
		if m.focusedTxIndex >= 0 && m.focusedTxIndex < len(m.transactions) {
			tx := m.transactions[m.focusedTxIndex]
			switch tx.GetState() {
			case transaction.StateRunning:
				add("[p]", "pause")
				add("[c]", "cancel")
			case transaction.StatePaused:
				// Both priority-paused and normally-paused support [p] to resume/swap.
				add("[p]", "resume")
				add("[c]", "cancel")
			case transaction.StateDone:
				add("[r]", "open report")
			case transaction.StateQueued:
				add("[s]", "start")
				add("[c]", "cancel")
			}
		}
		add("[n]", "new")

	case PanelQueue:
		add("[enter]", "focus")
		if m.queue.cursor >= 0 && m.queue.cursor < len(m.transactions) {
			tx := m.transactions[m.queue.cursor]
			switch tx.GetState() {
			case transaction.StateQueued:
				add("[s]", "start")
				add("[c]", "cancel")
			case transaction.StateRunning:
				add("[p]", "pause")
				add("[c]", "cancel")
			case transaction.StatePaused:
				// Both priority-paused and normally-paused support [p] to resume/swap.
				add("[p]", "resume")
				add("[c]", "cancel")
			}
		}
		add("[n]", "new")
	}

	add("[q]", "quit")

	// Join with a surface-backgrounded separator so the gaps between hints stay
	// the same colour as everything else.
	footer := strings.Join(parts, FooterSepStyle.Render("  "))
	return FooterStyle.Width(m.width).Render(footer)
}

// panelWidths returns the widths of the three panels.
func (m AppModel) panelWidths() (browser, detail, queue int) {
	total := m.width
	browser = total * 30 / 100
	queue = total * 35 / 100
	detail = total - browser - queue

	if browser < 20 {
		browser = 20
	}
	if queue < 18 {
		queue = 18
	}
	if detail < 20 {
		detail = 20
	}
	return
}

// resizePanels propagates new dimensions to sub-models.
func (m *AppModel) resizePanels() {
	bw, dw, qw := m.panelWidths()
	contentH := m.height - 4 // approximate header+footer

	m.browser.width = bw
	m.browser.height = contentH
	m.detail.width = dw
	m.detail.height = contentH
	m.queue.width = qw
	m.queue.height = contentH
}

// openFile opens a file with the system's default application.
func openFile(path string) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "open" // macOS default
	}
	cmd := exec.Command(editor, path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Start()
}

// humanize_bar is a local helper to avoid import cycle.
func humanize_bar(pct float64, width int) string {
	if width <= 0 {
		return ""
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	filled := int(pct * float64(width))
	empty := width - filled
	bar := ""
	for i := 0; i < filled; i++ {
		bar += "█"
	}
	for i := 0; i < empty; i++ {
		bar += "░"
	}
	return bar
}
