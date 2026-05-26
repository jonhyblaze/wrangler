// Package ui - browser.go implements the file browser panel (left).
//
// Layout:
//
//	QUICK ACCESS          ← always-visible bookmarks (volumes + common folders)
//	─────────────────
//	[C] CARD_A  28 GB
//	[D] SSD_01
//	  Desktop
//	  Movies
//	─────────────────
//	/current/path         ← directory navigator below
//	─────────────────
//	↑ ..
//	▸ DCIM/
//	▸ MISC/
//	  thumbnail.jpg       ← files shown; selectable as source only
package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/jonhyblaze/wrangler/internal/media"
	"github.com/jonhyblaze/wrangler/pkg/humanize"
)

// browserEntry is a single row in the unified entry list.
type browserEntry struct {
	name  string
	path  string

	// Type flags — only one should be true at a time (except isBookmark + isVolume).
	isDir        bool
	isParent     bool // ".." entry
	isHeader     bool // section label, not selectable
	isSeparator  bool // horizontal rule, not selectable
	isBookmark   bool // top-section quick-access entry

	// Volume metadata (set when isBookmark && isVolume).
	isVolume     bool
	isCameraCard bool
	freeBytes    int64
}

// BrowserModel is the file browser panel state.
type BrowserModel struct {
	currentPath string
	dirEntries  []browserEntry // contents of currentPath (reloaded on navigation)
	volumes     []media.VolumeInfo

	cursor        int
	width, height int

	// Selection state for new-transaction flow.
	selectedSource string
	selectingDest  bool
	selectedDests  []string // up to 2

	// Eject confirmation.
	ejectTarget  string
	confirmEject bool
	busyProcess  string // process name blocking eject (empty = unknown)
}

// NewBrowser creates a BrowserModel starting at /Volumes.
func NewBrowser(volumes []media.VolumeInfo) BrowserModel {
	m := BrowserModel{volumes: volumes}
	start := "/Volumes"
	if _, err := os.Stat(start); err != nil {
		home, _ := os.UserHomeDir()
		start = home
	}
	m.navigateTo(start)
	return m
}

// SetVolumes refreshes volume metadata (called when watcher detects a change).
func (m *BrowserModel) SetVolumes(vols []media.VolumeInfo) {
	m.volumes = vols
	m.reloadDir() // refresh in case a volume appeared/disappeared in currentPath
}

// ── Navigation ────────────────────────────────────────────────────────────────

func (m *BrowserModel) navigateTo(path string) {
	path = filepath.Clean(path)
	m.currentPath = path
	m.reloadDir()
	// Position cursor on first selectable entry.
	all := m.all()
	for i, e := range all {
		if !e.isHeader && !e.isSeparator {
			m.cursor = i
			return
		}
	}
	m.cursor = 0
}

// reloadDir refreshes m.dirEntries from the filesystem.
func (m *BrowserModel) reloadDir() {
	m.dirEntries = nil

	// ".." parent entry (not at root).
	if m.currentPath != "/" {
		m.dirEntries = append(m.dirEntries, browserEntry{
			name:     "..",
			path:     filepath.Dir(m.currentPath),
			isDir:    true,
			isParent: true,
		})
	}

	des, err := os.ReadDir(m.currentPath)
	if err != nil {
		m.dirEntries = append(m.dirEntries, browserEntry{
			name: fmt.Sprintf("(cannot read: %v)", err),
		})
		return
	}

	var dirs, files []browserEntry
	for _, de := range des {
		name := de.Name()
		if strings.HasPrefix(name, ".") {
			continue // skip hidden
		}
		fullPath := filepath.Join(m.currentPath, name)
		isDir := de.IsDir()
		e := browserEntry{name: name, path: fullPath, isDir: isDir}

		// Annotate /Volumes children with camera-card / free-space info.
		if m.currentPath == "/Volumes" && isDir {
			for _, v := range m.volumes {
				if v.MountPoint == fullPath {
					e.isVolume = true
					e.isCameraCard = v.IsCamera
					e.freeBytes = v.FreeBytes
					break
				}
			}
		}

		if isDir {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}

	sortByName := func(s []browserEntry) {
		sort.Slice(s, func(i, j int) bool {
			return strings.ToLower(s[i].name) < strings.ToLower(s[j].name)
		})
	}
	sortByName(dirs)
	sortByName(files)

	m.dirEntries = append(m.dirEntries, dirs...)
	m.dirEntries = append(m.dirEntries, files...)
}

// all returns the combined entry list: bookmarks + separator + dirEntries.
// Recomputed on every call (cheap; entries are small).
func (m BrowserModel) all() []browserEntry {
	var out []browserEntry

	// ── QUICK ACCESS header ──────────────────────────────────────────────────
	out = append(out, browserEntry{isHeader: true, name: "QUICK ACCESS"})

	// Removable volumes from watcher.
	for _, v := range m.volumes {
		out = append(out, browserEntry{
			name:         v.Name,
			path:         v.MountPoint,
			isDir:        true,
			isBookmark:   true,
			isVolume:     true,
			isCameraCard: v.IsCamera,
			freeBytes:    v.FreeBytes,
		})
	}

	// Common home-directory bookmarks.
	home, _ := os.UserHomeDir()
	bmFolders := []struct{ label, subdir string }{
		{"Desktop", "Desktop"},
		{"Movies", "Movies"},
		{"Pictures", "Pictures"},
		{"Music", "Music"},
		{"Downloads", "Downloads"},
	}
	for _, bm := range bmFolders {
		p := filepath.Join(home, bm.subdir)
		if _, err := os.Stat(p); err == nil {
			out = append(out, browserEntry{
				name:       bm.label,
				path:       p,
				isDir:      true,
				isBookmark: true,
			})
		}
	}

	// ── Separator ────────────────────────────────────────────────────────────
	out = append(out, browserEntry{isSeparator: true})

	// ── Current directory listing ─────────────────────────────────────────
	out = append(out, m.dirEntries...)

	return out
}

// NavigateInto opens the highlighted directory (or follows ".." / a bookmark).
func (m *BrowserModel) NavigateInto() bool {
	e, ok := m.current()
	if !ok || !e.isDir {
		return false
	}
	if e.isParent {
		m.NavigateUp()
		return true
	}
	m.navigateTo(e.path)
	// Always land in the directory listing (bottom section) after navigating
	// into any folder, whether from a bookmark/card or a child directory.
	m.jumpToFirstDirEntry()
	return true
}

// firstDirEntryIdx returns the index of the first selectable entry in the
// bottom (directory listing) section — the first entry after the separator.
// Returns -1 if the section is empty.
func (m *BrowserModel) firstDirEntryIdx() int {
	all := m.all()
	pastSep := false
	for i, e := range all {
		if e.isSeparator {
			pastSep = true
			continue
		}
		if pastSep && !e.isHeader && !e.isSeparator {
			return i
		}
	}
	return -1
}

// jumpToFirstDirEntry positions the cursor on the first selectable entry in
// the bottom section (actual directory listing).
func (m *BrowserModel) jumpToFirstDirEntry() {
	if idx := m.firstDirEntryIdx(); idx >= 0 {
		m.cursor = idx
	}
}

// NavigateUp goes to the parent directory, landing in the bottom section.
func (m *BrowserModel) NavigateUp() {
	parent := filepath.Dir(m.currentPath)
	if parent != m.currentPath {
		m.navigateTo(parent)
		m.jumpToFirstDirEntry()
	}
}

// MoveUp moves the cursor up, skipping headers and separators.
func (m *BrowserModel) MoveUp() {
	all := m.all()
	for m.cursor > 0 {
		m.cursor--
		e := all[m.cursor]
		if !e.isHeader && !e.isSeparator {
			return
		}
	}
}

// MoveDown moves the cursor down, skipping headers and separators.
func (m *BrowserModel) MoveDown() {
	all := m.all()
	for m.cursor < len(all)-1 {
		m.cursor++
		e := all[m.cursor]
		if !e.isHeader && !e.isSeparator {
			return
		}
	}
}

// GotoHome navigates to the user's home directory, landing in the bottom section.
func (m *BrowserModel) GotoHome() {
	if home, err := os.UserHomeDir(); err == nil {
		m.navigateTo(home)
		m.jumpToFirstDirEntry()
	}
}

// GotoVolumes navigates to /Volumes, landing in the bottom section.
func (m *BrowserModel) GotoVolumes() {
	m.navigateTo("/Volumes")
	m.jumpToFirstDirEntry()
}

// current returns the entry at the cursor position.
func (m *BrowserModel) current() (browserEntry, bool) {
	all := m.all()
	if m.cursor < 0 || m.cursor >= len(all) {
		return browserEntry{}, false
	}
	return all[m.cursor], true
}

// ── Selection ─────────────────────────────────────────────────────────────────

// Select handles [space] — picks source then destination.
// Returns (source, dests, ready=true) when a complete transaction can be queued.
// Files and directories are both valid sources; only directories are valid destinations.
func (m *BrowserModel) Select() (source string, dests []string, ready bool) {
	e, ok := m.current()
	if !ok || e.isHeader || e.isSeparator || e.isParent {
		return
	}

	if !m.selectingDest {
		// First selection: set as source (file OR directory).
		m.selectedSource = e.path
		m.selectingDest = true
		return
	}

	// Second+ selection: destination must be a directory.
	var target string
	if e.isDir {
		target = e.path
	} else {
		// File highlighted — use the directory we're browsing as destination.
		target = m.currentPath
	}

	if target == m.selectedSource {
		return // can't use source as destination
	}
	for _, d := range m.selectedDests {
		if d == target {
			return // already added
		}
	}
	m.selectedDests = append(m.selectedDests, target)

	// After the first destination is chosen the transaction is ready.
	// A second [space] press on a different path would add a second destination,
	// but we fire immediately after 1 to keep the flow fast.
	if len(m.selectedDests) >= 1 {
		src := m.selectedSource
		ds := make([]string, len(m.selectedDests))
		copy(ds, m.selectedDests)
		m.Reset()
		return src, ds, true
	}
	return
}

// Reset clears selection and eject state.
func (m *BrowserModel) Reset() {
	m.selectedSource = ""
	m.selectingDest = false
	m.selectedDests = nil
	m.confirmEject = false
	m.ejectTarget = ""
}

// StartEject marks the highlighted volume for eject confirmation.
func (m *BrowserModel) StartEject() string {
	e, ok := m.current()
	if !ok || !e.isVolume {
		return ""
	}
	m.ejectTarget = e.path
	m.confirmEject = true
	return e.path
}

// ConfirmEject clears the confirmation state and returns the target path.
func (m *BrowserModel) ConfirmEject() string {
	t := m.ejectTarget
	m.confirmEject = false
	m.ejectTarget = ""
	return t
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m BrowserModel) View() string {
	innerWidth := m.width - 4
	if innerWidth < 8 {
		innerWidth = 8
	}

	var lines []string

	// Panel title + current path breadcrumb.
	lines = append(lines, PanelTitleStyle.Render("FILE BROWSER"))
	lines = append(lines, AmberStyle.Render(humanize.ShortPath(m.currentPath, innerWidth)))
	lines = append(lines, DimStyle.Render(strings.Repeat("─", innerWidth)))

	// Selection status bar.
	if m.confirmEject {
		volName := humanize.ShortPath(m.ejectTarget, innerWidth-8)
		if m.busyProcess != "" {
			lines = append(lines, AmberStyle.Render("BUSY: "+m.busyProcess))
			lines = append(lines, RedStyle.Render("EJECT: "+volName))
			lines = append(lines, MutedStyle.Render("[f] force eject  [esc] cancel"))
		} else {
			lines = append(lines, RedStyle.Render("EJECT: "+volName))
			lines = append(lines, MutedStyle.Render("[y] confirm  [f] force  [esc] cancel"))
		}
		lines = append(lines, "")
	} else if m.selectingDest {
		src := humanize.ShortPath(m.selectedSource, innerWidth-6)
		lines = append(lines, AmberStyle.Render("SRC: "+src))
		lines = append(lines, MutedStyle.Render("[space] set dest  [esc] cancel"))
		lines = append(lines, "")
	} else {
		lines = append(lines, MutedStyle.Render("[space] select  [→] open  [←] back"))
		lines = append(lines, "")
	}

	headerLines := len(lines)
	visibleRows := m.height - headerLines - 1
	if visibleRows < 1 {
		visibleRows = 1
	}

	all := m.all()

	// Compute scroll offset to keep cursor visible.
	offset := 0
	if m.cursor >= visibleRows {
		offset = m.cursor - visibleRows + 1
	}
	end := offset + visibleRows
	if end > len(all) {
		end = len(all)
	}

	if offset > 0 {
		lines = append(lines, MutedStyle.Render(fmt.Sprintf(" ↑ %d more", offset)))
	}

	for i := offset; i < end; i++ {
		lines = append(lines, m.renderEntry(all[i], i, innerWidth))
	}

	below := len(all) - end
	if below > 0 {
		lines = append(lines, MutedStyle.Render(fmt.Sprintf(" ↓ %d more", below)))
	}

	return strings.Join(lines, "\n")
}

func (m BrowserModel) renderEntry(e browserEntry, idx int, width int) string {
	if e.isHeader {
		return PanelTitleStyle.Render(e.name)
	}
	if e.isSeparator {
		return DimStyle.Render(strings.Repeat("─", width))
	}

	selected := idx == m.cursor
	isSource := e.path != "" && e.path == m.selectedSource
	isDest := false
	for _, d := range m.selectedDests {
		if d == e.path {
			isDest = true
			break
		}
	}

	// Build icon + label.
	var icon string
	switch {
	case e.isParent:
		icon = "↑ "
	case e.isCameraCard:
		icon = "[C] "
	case e.isVolume:
		icon = "[D] "
	case e.isBookmark && e.isDir:
		icon = "  ~ "
	case e.isDir:
		icon = "  ▸ "
	default:
		icon = "    "
	}

	label := icon + e.name
	if e.isDir && !e.isParent {
		label += "/"
	}
	if len(label) > width-1 {
		label = label[:width-1]
	}

	// Right-side annotation (free space for volumes).
	annotation := ""
	if e.isVolume && e.freeBytes > 0 {
		annotation = MutedStyle.Render("  " + humanize.Bytes(e.freeBytes))
	}

	// Choose row style.
	var style lipgloss.Style
	switch {
	case isSource:
		icon = "→ "
		plain := strings.TrimLeft(label, " ↑▸[CDM~] ")
		label = icon + plain
		style = lipgloss.NewStyle().Foreground(ColorAmber).Bold(true)
	case isDest:
		icon = "✓ "
		plain := strings.TrimLeft(label, " ↑▸[CDM~] ")
		label = icon + plain
		style = lipgloss.NewStyle().Foreground(ColorGreen).Bold(true)
	case selected && e.isDir:
		style = ActiveItemStyle
	case selected:
		style = SelectedItemStyle
	case e.isParent:
		style = MutedStyle
	case e.isDir:
		style = lipgloss.NewStyle().Foreground(ColorText)
	default:
		style = DimStyle // files are secondary
	}

	return style.Render(label) + annotation
}
