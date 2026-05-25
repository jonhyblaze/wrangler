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
	transactions []*transaction.Transaction
	cursor       int
	offset       int
	width        int
	height       int
	focusedIndex int // index of the transaction shown in the detail panel
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

	return strings.Join(lines, "\n")
}

// renderRow renders a single transaction row.
func (m QueueModel) renderRow(snap transaction.TxSnapshot, idx int, width int) string {
	isCursor := idx == m.cursor
	isFocused := idx == m.focusedIndex

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

	// For active/verifying states, show compact progress.
	extraPart := ""
	if snap.State == transaction.StateRunning || snap.State == transaction.StatePaused {
		pct := snap.Progress.Percent()
		bar := humanize.ProgressBar(pct, 10)
		barColored := lipgloss.NewStyle().Foreground(color).Render(bar)
		extraPart = " " + barColored + fmt.Sprintf(" %.0f%%", pct*100)
		statePart = ""
	} else if snap.State == transaction.StateVerifying {
		if snap.Verify.FilesTotal > 0 {
			pct := float64(snap.Verify.FilesChecked) / float64(snap.Verify.FilesTotal)
			bar := humanize.ProgressBar(pct, 10)
			barColored := lipgloss.NewStyle().Foreground(color).Render(bar)
			extraPart = " " + barColored
		}
		statePart = ""
	}

	stateRendered := stateStyle.Render(statePart)
	row := prefix + idPart + stateRendered + extraPart

	// Truncate if too long.
	// Note: ANSI escape codes inflate len(), but for display purposes this is approximate.
	if len(row) > width+10 { // rough threshold accounting for escape codes
		row = row[:width+10]
	}

	if isCursor {
		return SelectedItemStyle.Render(prefix) + stateStyle.Render(idPart) + stateRendered + extraPart
	}
	return row
}
