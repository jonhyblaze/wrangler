// Package ui - browser.go implements the file browser panel (left).
package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/jonhyblaze/wrangler/internal/media"
	"github.com/jonhyblaze/wrangler/pkg/humanize"
)

// BrowserItem represents one row in the file browser.
type BrowserItem struct {
	label       string
	path        string
	isVolume    bool
	isCameraCard bool
	isHeader    bool // group header, not selectable
	freeBytes   int64
	totalBytes  int64
}

// BrowserModel is the file browser panel state.
type BrowserModel struct {
	volumes       []media.VolumeInfo
	bookmarks     []string
	items         []BrowserItem
	cursor        int
	width         int
	height        int
	selectedSource string
	selectingDest  bool
	selectedDests  []string // up to 2
	ejectTarget   string   // path being confirmed for eject
	confirmEject  bool
}

// NewBrowser creates a new BrowserModel.
func NewBrowser(volumes []media.VolumeInfo) BrowserModel {
	home, _ := os.UserHomeDir()
	bookmarks := []string{
		home,
		filepath.Join(home, "Movies"),
		filepath.Join(home, "Desktop"),
		filepath.Join(home, "Downloads"),
	}

	m := BrowserModel{
		volumes:   volumes,
		bookmarks: bookmarks,
	}
	m.rebuildItems()
	return m
}

// SetVolumes updates the volume list and rebuilds the item list.
func (m *BrowserModel) SetVolumes(vols []media.VolumeInfo) {
	m.volumes = vols
	m.rebuildItems()
}

// rebuildItems flattens volumes and bookmarks into a display list.
func (m *BrowserModel) rebuildItems() {
	m.items = nil

	// Volumes section.
	m.items = append(m.items, BrowserItem{label: "VOLUMES", isHeader: true})
	for _, v := range m.volumes {
		label := v.Name
		item := BrowserItem{
			label:        label,
			path:         v.MountPoint,
			isVolume:     true,
			isCameraCard: v.IsCamera,
			freeBytes:    v.FreeBytes,
			totalBytes:   v.TotalBytes,
		}
		m.items = append(m.items, item)
	}
	if len(m.volumes) == 0 {
		m.items = append(m.items, BrowserItem{label: "  (no volumes mounted)", isHeader: true})
	}

	// Bookmarks section.
	m.items = append(m.items, BrowserItem{label: "", isHeader: true}) // spacer
	m.items = append(m.items, BrowserItem{label: "BOOKMARKS", isHeader: true})
	for _, b := range m.bookmarks {
		if _, err := os.Stat(b); err == nil {
			m.items = append(m.items, BrowserItem{
				label:    filepath.Base(b),
				path:     b,
				isVolume: false,
			})
		}
	}

	// Clamp cursor.
	if m.cursor >= len(m.items) {
		m.cursor = max(0, len(m.items)-1)
	}
}

// MoveUp moves the cursor up, skipping headers.
func (m *BrowserModel) MoveUp() {
	for m.cursor > 0 {
		m.cursor--
		if !m.items[m.cursor].isHeader && m.items[m.cursor].path != "" {
			return
		}
	}
}

// MoveDown moves the cursor down, skipping headers.
func (m *BrowserModel) MoveDown() {
	for m.cursor < len(m.items)-1 {
		m.cursor++
		if !m.items[m.cursor].isHeader && m.items[m.cursor].path != "" {
			return
		}
	}
}

// SelectedItem returns the currently highlighted item (nil if header).
func (m *BrowserModel) SelectedItem() *BrowserItem {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return nil
	}
	item := &m.items[m.cursor]
	if item.isHeader || item.path == "" {
		return nil
	}
	return item
}

// Select handles Enter/Space on the current item.
// Returns (source, destinations, ready) — ready=true when a full transaction can be created.
func (m *BrowserModel) Select() (source string, dests []string, ready bool) {
	item := m.SelectedItem()
	if item == nil {
		return
	}

	if !m.selectingDest {
		// First selection: set as source.
		m.selectedSource = item.path
		m.selectingDest = true
		return
	}

	// Second/third selection: add as destination.
	// Don't allow selecting the same path as source.
	if item.path == m.selectedSource {
		return
	}
	// Don't allow duplicate destinations.
	for _, d := range m.selectedDests {
		if d == item.path {
			return
		}
	}

	m.selectedDests = append(m.selectedDests, item.path)

	// Allow up to 2 destinations; transaction is ready after at least 1.
	if len(m.selectedDests) >= 1 {
		// User can press Enter again to add a second destination, or [n] to confirm.
		// For simplicity: first destination press triggers "ready".
		src := m.selectedSource
		ds := make([]string, len(m.selectedDests))
		copy(ds, m.selectedDests)
		m.Reset()
		return src, ds, true
	}
	return
}

// AddDestAndCheck adds a destination and checks if we should create the transaction.
// Returns (dests, ready). If len(dests)==2, auto-ready.
func (m *BrowserModel) AddDestAndCheck() ([]string, bool) {
	return m.selectedDests, len(m.selectedDests) >= 2
}

// Reset clears the selection state.
func (m *BrowserModel) Reset() {
	m.selectedSource = ""
	m.selectingDest = false
	m.selectedDests = nil
	m.confirmEject = false
	m.ejectTarget = ""
}

// StartEject marks a volume for eject confirmation.
func (m *BrowserModel) StartEject() string {
	item := m.SelectedItem()
	if item == nil || !item.isVolume {
		return ""
	}
	m.ejectTarget = item.path
	m.confirmEject = true
	return item.path
}

// ConfirmEject returns the eject target and clears confirmation state.
func (m *BrowserModel) ConfirmEject() string {
	target := m.ejectTarget
	m.confirmEject = false
	m.ejectTarget = ""
	return target
}

// View renders the browser panel.
func (m BrowserModel) View() string {
	if m.width == 0 {
		return ""
	}

	innerWidth := m.width - 4 // account for border + padding
	if innerWidth < 1 {
		innerWidth = 1
	}

	var lines []string

	title := PanelTitleStyle.Render("FILE BROWSER")
	lines = append(lines, title)
	lines = append(lines, DimStyle.Render(strings.Repeat("─", innerWidth)))

	// Show selection hint.
	if m.selectingDest {
		hint := AmberStyle.Render(fmt.Sprintf("SOURCE: %s", humanize.ShortPath(m.selectedSource, innerWidth-9)))
		lines = append(lines, hint)
		lines = append(lines, MutedStyle.Render("Select destination(s), [esc] cancel"))
		lines = append(lines, "")
	}

	// Show eject confirmation.
	if m.confirmEject {
		lines = append(lines, RedStyle.Render("EJECT: "+humanize.ShortPath(m.ejectTarget, innerWidth-8)))
		lines = append(lines, MutedStyle.Render("[y] confirm  [f] force  [esc] cancel"))
		lines = append(lines, "")
	}

	visibleRows := m.height - len(lines) - 3
	if visibleRows < 1 {
		visibleRows = 1
	}

	// Scroll window.
	start := 0
	if m.cursor >= visibleRows {
		start = m.cursor - visibleRows + 1
	}
	end := start + visibleRows
	if end > len(m.items) {
		end = len(m.items)
	}

	for i := start; i < end; i++ {
		item := m.items[i]
		line := m.renderItem(item, i, innerWidth)
		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

// renderItem renders a single browser item.
func (m BrowserModel) renderItem(item BrowserItem, idx int, width int) string {
	if item.isHeader {
		if item.label == "" {
			return ""
		}
		return PanelTitleStyle.Render(item.label)
	}

	selected := idx == m.cursor
	isSource := item.path == m.selectedSource
	isDest := false
	for _, d := range m.selectedDests {
		if d == item.path {
			isDest = true
			break
		}
	}

	// Build label.
	prefix := "  "
	if item.isCameraCard {
		prefix = "  [C] "
	} else if item.isVolume {
		prefix = "  [D] "
	} else {
		prefix = "  ~/  "
	}

	label := item.label
	maxLabelWidth := width - len(prefix) - 1
	if len(label) > maxLabelWidth {
		label = label[:maxLabelWidth]
	}

	// Free space indicator (for volumes).
	var freeStr string
	if item.isVolume && item.freeBytes > 0 {
		freeStr = MutedStyle.Render(" " + humanize.Bytes(item.freeBytes))
	}

	row := prefix + label

	var style lipgloss.Style
	switch {
	case isSource:
		style = lipgloss.NewStyle().Foreground(ColorAmber).Bold(true)
		row = "→ " + row[2:] // replace prefix with arrow
	case isDest:
		style = lipgloss.NewStyle().Foreground(ColorGreen)
		row = "✓ " + row[2:]
	case selected:
		style = SelectedItemStyle
	default:
		style = lipgloss.NewStyle().Foreground(ColorText)
	}

	rendered := style.Render(row)
	if item.isVolume && freeStr != "" {
		rendered += freeStr
	}
	return rendered
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
