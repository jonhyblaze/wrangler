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

	watcher *media.Watcher
	keys    KeyMap

	// Eject state.
	confirmingEject bool
	forceEject      bool

	// Status message (shown in header for a moment).
	statusMsg string

	// Rsync unavailable warning.
	rsyncWarning string
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
		// Keep pinging for more progress if runner is still active.
		if m.activeRunner != nil {
			return m, m.activeRunner.NextProgressMsg()
		}
		return m, nil

	case transaction.RunnerDoneMsg:
		m.activeRunner = nil
		if msg.Err != nil {
			// Error already recorded in transaction.
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
		if m.activeVerifier != nil {
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
		// Advance queue to run next queued transaction.
		return m, m.advanceQueue()

	case media.EjectMsg:
		m.confirmingEject = false
		m.browser.confirmEject = false
		if msg.Result.Success {
			m.statusMsg = fmt.Sprintf("Ejected %s", msg.Result.MountPoint)
		} else if msg.Result.Err == media.ErrBusy {
			m.statusMsg = "Volume busy — press [f] to force eject"
			m.confirmingEject = true
			m.forceEject = true
		} else {
			m.statusMsg = fmt.Sprintf("Eject failed: %v", msg.Result.Err)
		}
		return m, m.watcher.NextVolumeMsg()

	case FocusTransactionMsg:
		if msg.Index >= 0 && msg.Index < len(m.transactions) {
			m.focusedTxIndex = msg.Index
			m.detail.SetTransaction(m.transactions[msg.Index])
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// handleKey processes key events.
func (m AppModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Global keys.
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.keys.Tab):
		m.activePanel = (m.activePanel + 1) % 3
		return m, nil

	case key.Matches(msg, m.keys.NewTransaction):
		// [n] in any panel: start a new transaction flow.
		if !m.browser.selectingDest {
			m.activePanel = PanelBrowser
			m.browser.Reset()
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
	switch {
	case key.Matches(msg, m.keys.Up):
		m.browser.MoveUp()

	case key.Matches(msg, m.keys.Down):
		m.browser.MoveDown()

	case key.Matches(msg, m.keys.Back):
		m.browser.Reset()
		m.confirmingEject = false
		m.forceEject = false

	case key.Matches(msg, m.keys.Eject):
		if !m.browser.selectingDest {
			target := m.browser.StartEject()
			if target != "" {
				m.confirmingEject = true
				m.forceEject = false
			}
		}

	case key.Matches(msg, m.keys.Confirm):
		// [y] confirms eject.
		if m.confirmingEject {
			target := m.browser.ConfirmEject()
			m.confirmingEject = false
			if target != "" {
				return m, media.EjectCmd(target, m.forceEject)
			}
		}

	case key.Matches(msg, m.keys.ForceEject):
		// [f] force eject.
		if m.confirmingEject && m.browser.ejectTarget != "" {
			target := m.browser.ConfirmEject()
			m.confirmingEject = false
			if target != "" {
				return m, media.EjectCmd(target, true)
			}
		}

	case key.Matches(msg, m.keys.Select):
		if m.browser.confirmEject {
			// Enter on eject confirmation = confirm.
			target := m.browser.ConfirmEject()
			m.confirmingEject = false
			if target != "" {
				return m, media.EjectCmd(target, m.forceEject)
			}
			return m, nil
		}

		src, dests, ready := m.browser.Select()
		if ready {
			tx := transaction.New(src, dests)
			m.transactions = append(m.transactions, tx)
			m.queue.SetTransactions(m.transactions)
			// Auto-focus the new transaction in detail panel.
			newIdx := len(m.transactions) - 1
			m.focusedTxIndex = newIdx
			m.detail.SetTransaction(m.transactions[newIdx])
			m.queue.focusedIndex = newIdx
			m.queue.cursor = newIdx
			// If nothing is running, start it.
			return m, m.advanceQueue()
		}
	}

	return m, nil
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
				}
			}
		case transaction.StatePaused:
			if m.activeRunner != nil {
				if err := m.activeRunner.Resume(); err == nil {
					_ = tx.SetState(transaction.StateRunning)
					return m, m.activeRunner.NextProgressMsg()
				}
			}
		}

	case key.Matches(msg, m.keys.Cancel):
		switch state {
		case transaction.StateRunning, transaction.StatePaused:
			if m.activeRunner != nil {
				m.activeRunner.Cancel()
				m.activeRunner = nil
				return m, m.advanceQueue()
			}
		case transaction.StateQueued:
			_ = tx.SetState(transaction.StateCancelled)
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

	case key.Matches(msg, m.keys.Select):
		focusMsg := m.queue.SelectCurrent()
		if focusMsg.Index >= 0 && focusMsg.Index < len(m.transactions) {
			m.focusedTxIndex = focusMsg.Index
			m.detail.SetTransaction(m.transactions[focusMsg.Index])
		}
	}

	return m, nil
}

// advanceQueue starts the next QUEUED transaction if nothing is running.
func (m *AppModel) advanceQueue() tea.Cmd {
	if m.activeRunner != nil {
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
	bm := m.browser
	bm.width = bw
	bm.height = contentH

	dm := m.detail
	dm.width = dw
	dm.height = contentH

	qm := m.queue
	qm.width = qw
	qm.height = contentH

	// Render panels with borders.
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

	// Calculate inner widths (width minus border and padding).
	bInner := bw - 4
	dInner := dw - 4
	qInner := qw - 4

	if bInner < 1 {
		bInner = 1
	}
	if dInner < 1 {
		dInner = 1
	}
	if qInner < 1 {
		qInner = 1
	}

	browserPanel := browserStyle.
		Width(bInner).
		Height(contentH - 2).
		Render(bm.View())

	detailPanel := detailStyle.
		Width(dInner).
		Height(contentH - 2).
		Render(dm.View())

	queuePanel := queueStyle.
		Width(qInner).
		Height(contentH - 2).
		Render(qm.View())

	content := lipgloss.JoinHorizontal(lipgloss.Top, browserPanel, detailPanel, queuePanel)

	return lipgloss.JoinVertical(lipgloss.Left, header, content, footer)
}

// renderHeader renders the top header bar.
func (m AppModel) renderHeader() string {
	left := lipgloss.NewStyle().
		Foreground(ColorAmber).
		Bold(true).
		Render("WRANGLER")

	// Show active transaction summary if one is running.
	right := ""
	for _, tx := range m.transactions {
		snap := tx.Snapshot()
		if snap.State == transaction.StateRunning || snap.State == transaction.StateVerifying {
			pct := snap.Progress.Percent()
			bar := lipgloss.NewStyle().Foreground(ColorAmber).
				Render(humanize_bar(pct, 8))
			right = fmt.Sprintf("%s %s %.0f%%", snap.ID, bar, pct*100)
			break
		}
	}

	if m.statusMsg != "" {
		right = MutedStyle.Render(m.statusMsg)
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}

	header := left + strings.Repeat(" ", gap) + right
	return HeaderStyle.Width(m.width - 2).Render(header)
}

// renderFooter renders the bottom keybinding footer.
func (m AppModel) renderFooter() string {
	var parts []string

	parts = append(parts, MutedStyle.Render("[tab]")+" switch panel")
	parts = append(parts, MutedStyle.Render("[n]")+" new transaction")

	switch m.activePanel {
	case PanelBrowser:
		if m.browser.selectingDest {
			parts = append(parts, MutedStyle.Render("[enter]")+" select dest")
			parts = append(parts, MutedStyle.Render("[esc]")+" cancel")
		} else {
			parts = append(parts, MutedStyle.Render("[e]")+" eject")
		}
	case PanelDetail:
		if m.focusedTxIndex >= 0 && m.focusedTxIndex < len(m.transactions) {
			state := m.transactions[m.focusedTxIndex].GetState()
			switch state {
			case transaction.StateRunning:
				parts = append(parts, MutedStyle.Render("[p]")+" pause")
				parts = append(parts, MutedStyle.Render("[c]")+" cancel")
			case transaction.StatePaused:
				parts = append(parts, MutedStyle.Render("[p]")+" resume")
				parts = append(parts, MutedStyle.Render("[c]")+" cancel")
			case transaction.StateDone:
				parts = append(parts, MutedStyle.Render("[r]")+" open report")
			}
		}
	case PanelQueue:
		parts = append(parts, MutedStyle.Render("[enter]")+" focus")
	}

	parts = append(parts, MutedStyle.Render("[q]")+" quit")

	footer := strings.Join(parts, "  ")
	return FooterStyle.Width(m.width - 2).Render(footer)
}

// panelWidths returns the widths of the three panels.
func (m AppModel) panelWidths() (browser, detail, queue int) {
	total := m.width
	browser = total * 22 / 100
	queue = total * 28 / 100
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
