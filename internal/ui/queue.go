// Package ui - queue.go implements the transaction queue panel (right).
package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/jonhyblaze/wrangler/internal/transaction"
	"github.com/jonhyblaze/wrangler/pkg/humanize"
)

// FocusTransactionMsg signals that a transaction was selected in the queue.
type FocusTransactionMsg struct {
	Index int
}

// QueueModel is the transaction queue panel state.
type QueueModel struct {
	transactions      []*transaction.Transaction
	cursor            int
	offset            int
	width             int
	height            int
	focusedIndex      int    // index of the transaction shown in the detail panel
	priorityPausedID  string // ID of the transaction paused for priority-start (auto-resume pending)
}

// NewQueue creates a new QueueModel.
func NewQueue() QueueModel {
	return QueueModel{focusedIndex: -1}
}

// SetTransactions updates the transaction list.
func (m *QueueModel) SetTransactions(txs []*transaction.Transaction) {
	m.transactions = txs
	// Clamp cursor.
	if m.cursor >= len(txs) {
		m.cursor = max(0, len(txs)-1)
	}
}

// MoveUp moves the cursor up.
func (m *QueueModel) MoveUp() {
	if m.cursor > 0 {
		m.cursor--
	}
}

// MoveDown moves the cursor down.
func (m *QueueModel) MoveDown() {
	if m.cursor < len(m.transactions)-1 {
		m.cursor++
	}
}

// SelectCurrent returns a FocusTransactionMsg for the currently highlighted item.
func (m *QueueModel) SelectCurrent() FocusTransactionMsg {
	m.focusedIndex = m.cursor
	return FocusTransactionMsg{Index: m.cursor}
}

// SelectedTx returns the currently highlighted transaction (nil if list empty).
func (m *QueueModel) SelectedTx() *transaction.Transaction {
	if len(m.transactions) == 0 || m.cursor < 0 || m.cursor >= len(m.transactions) {
		return nil
	}
	return m.transactions[m.cursor]
}

// SetPriorityPausedID records the ID of the transaction that was paused to
// make room for a priority start. Pass "" to clear.
func (m *QueueModel) SetPriorityPausedID(id string) {
	m.priorityPausedID = id
}

// View renders the queue panel.
func (m QueueModel) View() string {
	innerWidth := m.width - 4
	if innerWidth < 10 {
		innerWidth = 10
	}

	var lines []string
	lines = append(lines, PanelTitleStyle.Render("TRANSACTION QUEUE"))
	lines = append(lines, DimStyle.Render(strings.Repeat("─", innerWidth)))
	lines = append(lines, "")

	if len(m.transactions) == 0 {
		lines = append(lines, MutedStyle.Render("No transactions."))
		lines = append(lines, MutedStyle.Render("[n] new transaction"))
		return strings.Join(lines, "\n")
	}

	headerLines := len(lines)
	visibleRows := m.height - headerLines - 2
	if visibleRows < 1 {
		visibleRows = 1
	}

	// Scroll to keep cursor visible.
	offset := m.offset
	if m.cursor < offset {
		offset = m.cursor
	}
	if m.cursor >= offset+visibleRows {
		offset = m.cursor - visibleRows + 1
	}

	// Show "more above" indicator.
	if offset > 0 {
		lines = append(lines, MutedStyle.Render(fmt.Sprintf("  ↑ %d more", offset)))
	}

	end := offset + visibleRows
	if end > len(m.transactions) {
		end = len(m.transactions)
	}

	for i := offset; i < end; i++ {
		tx := m.transactions[i]
		snap := tx.Snapshot()
		row := m.renderRow(snap, i, innerWidth)
		lines = append(lines, row)
	}

	// Show "more below" indicator.
	below := len(m.transactions) - end
	if below > 0 {
		lines = append(lines, MutedStyle.Render(fmt.Sprintf("  ↓ %d more", below)))
	}

	// Contextual action hint for the highlighted transaction.
	if m.cursor >= 0 && m.cursor < len(m.transactions) {
		snap := m.transactions[m.cursor].Snapshot()
		var hint string
		switch snap.State {
		case transaction.StateQueued:
			hint = "[s] start  [c] cancel"
		case transaction.StateRunning:
			hint = "[p] pause  [c] cancel"
		case transaction.StatePaused:
			// Both normally-paused and priority-paused support [p] resume/swap.
			hint = "[p] resume  [c] cancel"
		}
		if hint != "" {
			lines = append(lines, "")
			lines = append(lines, MutedStyle.Render(hint))
		}
	}

	return strings.Join(lines, "\n")
}

// renderRow renders a single transaction row.
func (m QueueModel) renderRow(snap transaction.TxSnapshot, idx int, width int) string {
	isCursor := idx == m.cursor
	isFocused := idx == m.focusedIndex
	isPriorityPaused := snap.ID == m.priorityPausedID && m.priorityPausedID != ""

	color := StateColor(snap.State)
	icon := snap.State.Icon()
	stateStyle := lipgloss.NewStyle().Foreground(color)

	// Prefix: cursor / focused indicator.
	prefix := "  "
	if isFocused {
		prefix = "▶ "
	}
	if isCursor {
		prefix = "> "
	}

	// ID + icon.
	idPart := fmt.Sprintf("%s  %s ", snap.ID, icon)
	statePart := snap.State.String()

	// For active/verifying states replace the state label with compact progress.
	extraPart := ""
	if snap.State == transaction.StateRunning || snap.State == transaction.StatePaused {
		pct := snap.Progress.Percent()
		bar := humanize.ProgressBar(pct, 8)
		barColored := lipgloss.NewStyle().Foreground(color).Render(bar)
		extraPart = " " + barColored + fmt.Sprintf(" %.0f%%", pct*100)
		statePart = ""
		// If this is the priority-paused transaction, append a resuming badge.
		if isPriorityPaused {
			extraPart += MutedStyle.Render(" ↩")
		}
	} else if snap.State == transaction.StateVerifying {
		if snap.Verify.FilesTotal > 0 {
			// Destination verification phase — show percentage.
			pct := float64(snap.Verify.FilesChecked) / float64(snap.Verify.FilesTotal)
			bar := humanize.ProgressBar(pct, 8)
			barColored := lipgloss.NewStyle().Foreground(color).Render(bar)
			extraPart = " " + barColored + fmt.Sprintf(" %.0f%%", pct*100)
		} else if snap.Verify.FilesChecked > 0 {
			// Source hashing phase — total unknown yet, show count.
			extraPart = fmt.Sprintf(" %s files", humanize.Comma(snap.Verify.FilesChecked))
		}
		statePart = ""
	}

	stateRendered := stateStyle.Render(statePart)

	var row string
	if isCursor {
		row = SelectedItemStyle.Render(prefix) + stateStyle.Render(idPart) + stateRendered + extraPart
	} else {
		// Apply state colour to the ID + icon so the state is immediately
		// visible on every row, not just the cursor row. Without this the icon
		// (⏸ vs ↓) and the ID text are all rendered in the plain terminal
		// foreground colour, making a paused-at-0% row visually identical to
		// a running-at-0% row.
		row = prefix + stateStyle.Render(idPart) + stateRendered + extraPart
	}

	// MaxWidth truncates correctly — it uses ansi.Truncate internally which
	// respects multi-byte runes (e.g. █ ░) and ANSI escape codes, unlike the
	// previous raw byte-slice which could corrupt progress-bar characters.
	return lipgloss.NewStyle().MaxWidth(width).Render(row)
}
