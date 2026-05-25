// Package ui - detail.go implements the transaction detail panel (center).
package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/jonhyblaze/wrangler/internal/transaction"
	"github.com/jonhyblaze/wrangler/pkg/humanize"
)

// DetailModel is the transaction detail panel state.
type DetailModel struct {
	tx     *transaction.Transaction
	width  int
	height int
}

// NewDetail creates a new DetailModel.
func NewDetail() DetailModel {
	return DetailModel{}
}

// SetTransaction sets the transaction to display.
func (m *DetailModel) SetTransaction(tx *transaction.Transaction) {
	m.tx = tx
}

// View renders the detail panel.
func (m DetailModel) View() string {
	innerWidth := m.width - 4
	if innerWidth < 10 {
		innerWidth = 10
	}

	if m.tx == nil {
		return m.renderEmpty(innerWidth)
	}

	snap := m.tx.Snapshot()

	switch snap.State {
	case transaction.StateQueued:
		return m.renderQueued(snap, innerWidth)
	case transaction.StateRunning:
		return m.renderRunning(snap, innerWidth)
	case transaction.StatePaused:
		return m.renderPaused(snap, innerWidth)
	case transaction.StateVerifying:
		return m.renderVerifying(snap, innerWidth)
	case transaction.StateDone:
		return m.renderDone(snap, innerWidth)
	case transaction.StateFailed:
		return m.renderFailed(snap, innerWidth)
	case transaction.StateCancelled:
		return m.renderCancelled(snap, innerWidth)
	default:
		return m.renderEmpty(innerWidth)
	}
}

func (m DetailModel) renderEmpty(width int) string {
	var lines []string
	lines = append(lines, PanelTitleStyle.Render("TRANSACTION DETAIL"))
	lines = append(lines, DimStyle.Render(strings.Repeat("─", width)))
	lines = append(lines, "")

	hint := MutedStyle.Render("No transaction selected.")
	lines = append(lines, hint)
	lines = append(lines, MutedStyle.Render("Press [n] to create one."))
	return strings.Join(lines, "\n")
}

func (m DetailModel) renderQueued(snap transaction.TxSnapshot, width int) string {
	var lines []string
	lines = append(lines, PanelTitleStyle.Render("TRANSACTION DETAIL"))
	lines = append(lines, DimStyle.Render(strings.Repeat("─", width)))
	lines = append(lines, "")
	lines = append(lines, BlueStyle.Render(fmt.Sprintf("%s  %s", snap.ID, snap.State.Icon()+" QUEUED")))
	lines = append(lines, "")
	lines = append(lines, m.renderSourceDest(snap, width)...)
	lines = append(lines, "")
	lines = append(lines, MutedStyle.Render("[c] cancel"))
	return strings.Join(lines, "\n")
}

func (m DetailModel) renderRunning(snap transaction.TxSnapshot, width int) string {
	var lines []string
	lines = append(lines, PanelTitleStyle.Render("TRANSACTION DETAIL"))
	lines = append(lines, DimStyle.Render(strings.Repeat("─", width)))
	lines = append(lines, "")
	lines = append(lines, AmberStyle.Render(snap.ID+"  "+snap.State.Icon()+" RUNNING"))
	lines = append(lines, "")
	lines = append(lines, m.renderSourceDest(snap, width)...)
	lines = append(lines, "")

	// Progress bar.
	barWidth := width - 2
	pct := snap.Progress.Percent()
	bar := humanize.ProgressBar(pct, barWidth)
	lines = append(lines, lipgloss.NewStyle().Foreground(ColorAmber).Render(bar))
	lines = append(lines, fmt.Sprintf("%.0f%%  %s  ETA %s",
		pct*100,
		humanize.Speed(snap.Progress.SpeedBPS),
		humanize.Duration(time.Duration(snap.Progress.ETASecs)*time.Second),
	))
	lines = append(lines, fmt.Sprintf("%s / %s files",
		humanize.Comma(snap.Progress.FilesDone),
		humanize.Comma(snap.Progress.FilesTotal),
	))
	lines = append(lines, fmt.Sprintf("%s / %s",
		humanize.Bytes(snap.Progress.BytesDone),
		humanize.Bytes(snap.Progress.BytesTotal),
	))
	if snap.Progress.CurrentFile != "" {
		lines = append(lines, MutedStyle.Render(humanize.ShortPath(snap.Progress.CurrentFile, width)))
	}
	lines = append(lines, "")
	lines = append(lines, MutedStyle.Render("[p] pause  [c] cancel"))
	return strings.Join(lines, "\n")
}

func (m DetailModel) renderPaused(snap transaction.TxSnapshot, width int) string {
	var lines []string
	lines = append(lines, PanelTitleStyle.Render("TRANSACTION DETAIL"))
	lines = append(lines, DimStyle.Render(strings.Repeat("─", width)))
	lines = append(lines, "")
	lines = append(lines, WhiteStyle.Render(snap.ID+"  ⏸ PAUSED"))
	lines = append(lines, "")
	lines = append(lines, m.renderSourceDest(snap, width)...)
	lines = append(lines, "")

	// Show last known progress.
	barWidth := width - 2
	pct := snap.Progress.Percent()
	bar := humanize.ProgressBar(pct, barWidth)
	lines = append(lines, lipgloss.NewStyle().Foreground(ColorWhite).Render(bar))
	lines = append(lines, fmt.Sprintf("%.0f%%  paused", pct*100))
	lines = append(lines, fmt.Sprintf("%s / %s files",
		humanize.Comma(snap.Progress.FilesDone),
		humanize.Comma(snap.Progress.FilesTotal),
	))
	lines = append(lines, "")
	lines = append(lines, MutedStyle.Render("[p] resume  [c] cancel"))
	return strings.Join(lines, "\n")
}

func (m DetailModel) renderVerifying(snap transaction.TxSnapshot, width int) string {
	var lines []string
	lines = append(lines, PanelTitleStyle.Render("TRANSACTION DETAIL"))
	lines = append(lines, DimStyle.Render(strings.Repeat("─", width)))
	lines = append(lines, "")
	lines = append(lines, AmberStyle.Render(snap.ID+"  ◌ VERIFYING"))
	lines = append(lines, "")
	lines = append(lines, m.renderSourceDest(snap, width)...)
	lines = append(lines, "")
	lines = append(lines, MutedStyle.Render("Running xxHash verification pass..."))

	if snap.Verify.FilesTotal > 0 {
		pct := float64(snap.Verify.FilesChecked) / float64(snap.Verify.FilesTotal)
		barWidth := width - 2
		bar := humanize.ProgressBar(pct, barWidth)
		lines = append(lines, lipgloss.NewStyle().Foreground(ColorGreen).Render(bar))
		lines = append(lines, fmt.Sprintf("%s / %s files verified",
			humanize.Comma(snap.Verify.FilesChecked),
			humanize.Comma(snap.Verify.FilesTotal),
		))
	}
	return strings.Join(lines, "\n")
}

func (m DetailModel) renderDone(snap transaction.TxSnapshot, width int) string {
	var lines []string
	lines = append(lines, PanelTitleStyle.Render("TRANSACTION DETAIL"))
	lines = append(lines, DimStyle.Render(strings.Repeat("─", width)))
	lines = append(lines, "")
	lines = append(lines, GreenStyle.Render(snap.ID+"  ✓ VERIFIED"))
	lines = append(lines, "")
	lines = append(lines, m.renderSourceDest(snap, width)...)
	lines = append(lines, "")
	lines = append(lines, GreenStyle.Render("VERIFICATION PASSED ✓"))
	lines = append(lines, fmt.Sprintf("%s files checked", humanize.Comma(snap.Verify.FilesChecked)))
	lines = append(lines, fmt.Sprintf("xxHash (64-bit)"))
	if snap.Verify.Duration > 0 {
		lines = append(lines, fmt.Sprintf("verify time: %s", humanize.Duration(snap.Verify.Duration)))
	}
	lines = append(lines, "")

	if !snap.FinishedAt.IsZero() && !snap.CreatedAt.IsZero() {
		dur := snap.FinishedAt.Sub(snap.CreatedAt)
		lines = append(lines, MutedStyle.Render("Total time: "+humanize.Duration(dur)))
	}
	lines = append(lines, MutedStyle.Render(fmt.Sprintf("%s  /  %s",
		humanize.Comma(snap.Progress.FilesTotal)+" files",
		humanize.Bytes(snap.Progress.BytesTotal),
	)))

	if len(snap.Report.Paths) > 0 {
		lines = append(lines, "")
		lines = append(lines, MutedStyle.Render("Report: "+humanize.ShortPath(snap.Report.Paths[0], width)))
		lines = append(lines, MutedStyle.Render("[r] open report"))
	}
	return strings.Join(lines, "\n")
}

func (m DetailModel) renderFailed(snap transaction.TxSnapshot, width int) string {
	var lines []string
	lines = append(lines, PanelTitleStyle.Render("TRANSACTION DETAIL"))
	lines = append(lines, DimStyle.Render(strings.Repeat("─", width)))
	lines = append(lines, "")
	lines = append(lines, RedStyle.Render(snap.ID+"  ✗ FAILED"))
	lines = append(lines, "")
	lines = append(lines, m.renderSourceDest(snap, width)...)
	lines = append(lines, "")
	lines = append(lines, RedStyle.Render("FAILED ✗"))

	if snap.Err != nil {
		// Split on embedded newlines first, then soft-wrap each paragraph at width.
		for _, para := range strings.Split(snap.Err.Error(), "\n") {
			for _, wrapped := range softWrap(para, width) {
				lines = append(lines, RedStyle.Render(wrapped))
			}
		}
	}

	if len(snap.Verify.Mismatches) > 0 {
		lines = append(lines, "")
		lines = append(lines, MutedStyle.Render(fmt.Sprintf("Mismatched files (%d):", len(snap.Verify.Mismatches))))
		for i, mm := range snap.Verify.Mismatches {
			if i >= 5 {
				lines = append(lines, MutedStyle.Render(fmt.Sprintf("  ... and %d more", len(snap.Verify.Mismatches)-5)))
				break
			}
			lines = append(lines, MutedStyle.Render("  "+humanize.ShortPath(mm, width-4)))
		}
	}
	return strings.Join(lines, "\n")
}

// softWrap splits s into lines of at most maxWidth runes, breaking at spaces
// where possible. Returns at least one element (an empty string for empty input).
func softWrap(s string, maxWidth int) []string {
	if maxWidth < 4 {
		maxWidth = 4
	}
	if s == "" {
		return []string{""}
	}

	var out []string
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}

	cur := ""
	for _, w := range words {
		// A single word longer than maxWidth — hard-break it.
		for len(w) > maxWidth {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			out = append(out, w[:maxWidth])
			w = w[maxWidth:]
		}
		if cur == "" {
			cur = w
		} else if len(cur)+1+len(w) <= maxWidth {
			cur += " " + w
		} else {
			out = append(out, cur)
			cur = w
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func (m DetailModel) renderCancelled(snap transaction.TxSnapshot, width int) string {
	var lines []string
	lines = append(lines, PanelTitleStyle.Render("TRANSACTION DETAIL"))
	lines = append(lines, DimStyle.Render(strings.Repeat("─", width)))
	lines = append(lines, "")
	lines = append(lines, MutedStyle.Render(snap.ID+"  ⊘ CANCELLED"))
	return strings.Join(lines, "\n")
}

// renderSourceDest returns formatted source and destination lines.
func (m DetailModel) renderSourceDest(snap transaction.TxSnapshot, width int) []string {
	var lines []string
	lines = append(lines, MutedStyle.Render("Source:"))
	lines = append(lines, "  "+humanize.ShortPath(snap.Source, width-4))
	lines = append(lines, MutedStyle.Render("Destinations:"))
	for _, d := range snap.Destinations {
		lines = append(lines, "  "+humanize.ShortPath(d, width-4))
	}
	return lines
}
