// Package ui - infopopup.go implements the file/folder info popup overlay.
package ui

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jonhyblaze/wrangler/internal/meta"
	"github.com/jonhyblaze/wrangler/pkg/humanize"
)

const (
	// popupOuterW is the total outer width of the popup (border + padding + content).
	// With 1-char border each side and 1-char padding each side:
	//   inner content width = popupOuterW - 2 (border) - 2 (padding) = popupInnerW
	popupOuterW = 50
	popupInnerW = 46 // popupOuterW - 4
	popupLabelW = 12
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// InfoPopupModel is the state of the info popup overlay.
type InfoPopupModel struct {
	path      string
	info      *meta.Meta
	loading   bool
	spinFrame int
	copiedKey string // label of last copied item (cleared after timeout)
}

// InfoCopiedMsg is sent when pbcopy completes.
type InfoCopiedMsg struct{ Key string }

// InfoCopiedClearMsg clears the "✓ Copied" indicator after a timeout.
type InfoCopiedClearMsg struct{}

// NewInfoPopup creates an InfoPopupModel and fires the async metadata fetch.
func NewInfoPopup(path string) (InfoPopupModel, tea.Cmd) {
	m := InfoPopupModel{path: path, loading: true}
	return m, tea.Batch(meta.FetchCmd(path), SpinCmd(path))
}

// SpinCmd fires a meta.SpinTickMsg after 80 ms to advance the spinner.
func SpinCmd(path string) tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg {
		return meta.SpinTickMsg{Path: path}
	})
}

// copyClearCmd fires InfoCopiedClearMsg after 1.5 s.
func copyClearCmd() tea.Cmd {
	return tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg {
		return InfoCopiedClearMsg{}
	})
}

// copyCmd puts text on the macOS clipboard via pbcopy.
func copyCmd(key, text string) tea.Cmd {
	return func() tea.Msg {
		c := exec.Command("pbcopy")
		c.Stdin = strings.NewReader(text)
		_ = c.Run()
		return InfoCopiedMsg{Key: key}
	}
}

// ── View ──────────────────────────────────────────────────────────────────────

var popupStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(ColorAmber).
	Padding(0, 1).
	Background(ColorSurface)

// View renders the popup box.
func (m InfoPopupModel) View() string {
	var sb strings.Builder

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorText)
	sep := DimStyle.Render(strings.Repeat("─", popupInnerW))

	sb.WriteString(titleStyle.Render("INFO"))
	sb.WriteString("\n")
	sb.WriteString(sep)
	sb.WriteString("\n")

	if m.loading {
		frame := spinnerFrames[m.spinFrame%len(spinnerFrames)]
		sb.WriteString(AmberStyle.Render(frame + "  Loading…"))
		sb.WriteString("\n")
	} else if m.info == nil || m.info.NotFound {
		sb.WriteString(RedStyle.Render("  File not found."))
		sb.WriteString("\n")
	} else {
		writeInfoContent(&sb, m.info)
	}

	// Footer.
	sb.WriteString("\n")
	if m.copiedKey != "" {
		sb.WriteString(GreenStyle.Render("✓ Copied " + m.copiedKey))
		sb.WriteString("\n")
	} else if m.info != nil && !m.info.NotFound {
		sb.WriteString(MutedStyle.Render("[s] set source  [d] add dest  [c] copy path"))
		sb.WriteString("\n")
		sb.WriteString(MutedStyle.Render("[b] copy breadcrumb  [m] copy full meta"))
		sb.WriteString("\n")
	}
	sb.WriteString(MutedStyle.Render("[esc] / [i]  close"))

	return popupStyle.Width(popupOuterW).Render(sb.String())
}

// writeInfoContent writes metadata rows for a loaded Meta into sb.
func writeInfoContent(sb *strings.Builder, i *meta.Meta) {
	// Breadcrumb path.
	bc := meta.Breadcrumb(i.Path, popupInnerW)
	sb.WriteString(AmberStyle.Render(bc))
	sb.WriteString("\n\n")

	// ── Common fields ─────────────────────────────────────────────────────────
	writeField(sb, "Kind", i.Kind)
	writeField(sb, "Size", popupFormatSize(i))
	if !i.Modified.IsZero() {
		writeField(sb, "Modified", popupFormatDate(i.Modified))
	}
	if !i.Created.IsZero() {
		writeField(sb, "Created", popupFormatDate(i.Created))
	}
	writeField(sb, "Permissions", i.Permissions)
	if i.Owner != "" {
		writeField(sb, "Owner", i.Owner)
	}
	if i.Volume != "" {
		vol := i.Volume
		if i.Filesystem != "" {
			vol += "  (" + i.Filesystem + ")"
		}
		writeField(sb, "Volume", vol)
	}

	// ── Folder ───────────────────────────────────────────────────────────────
	if i.Type == meta.FileTypeFolder {
		writeField(sb, "Contains", popupFormatContains(i))
	}

	// ── Video ─────────────────────────────────────────────────────────────────
	if i.Type == meta.FileTypeVideo {
		sb.WriteString("\n")
		if codecs := popupFormatCodecs(i); codecs != "" {
			writeField(sb, "Codecs", codecs)
		}
		if i.Duration > 0 {
			writeField(sb, "Duration", popupFormatDuration(i.Duration))
		}
		if i.Width > 0 && i.Height > 0 {
			writeField(sb, "Resolution", fmt.Sprintf("%d × %d", i.Width, i.Height))
		}
		if i.Framerate > 0 {
			writeField(sb, "Framerate", popupFormatFPS(i.Framerate))
		}
		if i.Bitrate > 0 {
			writeField(sb, "Bitrate", popupFormatBitrate(i.Bitrate))
		}
		if i.AudioChannels > 0 || i.AudioSampleRate > 0 || i.AudioCodec != "" {
			writeField(sb, "Audio", popupFormatAudio(i))
		}
	}

	// ── Audio ─────────────────────────────────────────────────────────────────
	if i.Type == meta.FileTypeAudio {
		sb.WriteString("\n")
		if i.AudioCodec != "" {
			writeField(sb, "Codec", i.AudioCodec)
		}
		if i.Duration > 0 {
			writeField(sb, "Duration", popupFormatDuration(i.Duration))
		}
		if i.AudioChannels > 0 || i.AudioSampleRate > 0 {
			writeField(sb, "Audio", popupFormatAudio(i))
		}
		if i.Bitrate > 0 {
			writeField(sb, "Bitrate", popupFormatBitrate(i.Bitrate))
		}
		if i.Authors != "" {
			writeField(sb, "Authors", i.Authors)
		}
	}

	// ── Image ─────────────────────────────────────────────────────────────────
	if i.Type == meta.FileTypeImage {
		if i.Width > 0 && i.Height > 0 {
			sb.WriteString("\n")
			writeField(sb, "Dimensions", fmt.Sprintf("%d × %d", i.Width, i.Height))
		}
	}

	// ── Camera / EXIF ─────────────────────────────────────────────────────────
	if i.CameraMake != "" || i.CameraModel != "" {
		sb.WriteString("\n")
		camera := i.CameraMake
		if i.CameraModel != "" {
			if camera != "" {
				camera += " / " + i.CameraModel
			} else {
				camera = i.CameraModel
			}
		}
		writeField(sb, "Camera", camera)
		if i.Lens != "" {
			writeField(sb, "Lens", i.Lens)
		}
		if i.Shutter != "" || i.Aperture != "" || i.ISO > 0 {
			var exp string
			if i.Shutter != "" {
				exp = i.Shutter
			}
			if i.Aperture != "" {
				if exp != "" {
					exp += "  " + i.Aperture
				} else {
					exp = i.Aperture
				}
			}
			if i.ISO > 0 {
				exp += fmt.Sprintf("  ISO %d", i.ISO)
			}
			writeField(sb, "Exposure", exp)
		}
		if i.FocalLength != "" {
			writeField(sb, "Focal len", i.FocalLength)
		}
	}

	// ── Spotlight note ────────────────────────────────────────────────────────
	if i.SpotlightNote != "" {
		sb.WriteString("\n")
		writeField(sb, "Note", i.SpotlightNote)
	}
}

// writeField writes one label + value row.
func writeField(sb *strings.Builder, label, value string) {
	if value == "" {
		return
	}
	lbl := MutedStyle.Render(fmt.Sprintf("%-*s", popupLabelW, label))
	maxVal := popupInnerW - popupLabelW - 1
	if maxVal < 4 {
		maxVal = 4
	}
	// Truncate long values (rune count approximation for ASCII-heavy content).
	if len(value) > maxVal {
		value = value[:maxVal-1] + "…"
	}
	sb.WriteString(lbl + " " + value + "\n")
}

// ── Render helpers ────────────────────────────────────────────────────────────

func popupFormatSize(m *meta.Meta) string {
	if m.Type == meta.FileTypeFolder {
		if m.SizeOnDisk > 0 {
			return humanize.Bytes(m.SizeOnDisk) + " on disk"
		}
		return "unknown"
	}
	if m.Size > 0 {
		s := humanize.Bytes(m.Size)
		if m.SizeOnDisk > 0 && m.SizeOnDisk != m.Size {
			s += "  (" + humanize.Bytes(m.SizeOnDisk) + " on disk)"
		}
		return s
	}
	return "unknown"
}

func popupFormatContains(m *meta.Meta) string {
	var parts []string
	if m.FileCount > 0 {
		parts = append(parts, fmt.Sprintf("%d files", m.FileCount))
	}
	if m.FolderCount > 0 {
		parts = append(parts, fmt.Sprintf("%d folders", m.FolderCount))
	}
	if m.HiddenCount > 0 {
		parts = append(parts, fmt.Sprintf("%d hidden", m.HiddenCount))
	}
	if len(parts) == 0 {
		return "empty"
	}
	return strings.Join(parts, ", ")
}

func popupFormatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("Jan 2 2006, 15:04")
}

func popupFormatFPS(fps float64) string {
	if fps == 0 {
		return ""
	}
	// Common cinema rates.
	type knownFPS struct {
		val   float64
		label string
		tol   float64
	}
	known := []knownFPS{
		{23.976, "23.976 fps", 0.01},
		{24, "24 fps", 0.01},
		{25, "25 fps", 0.01},
		{29.97, "29.97 fps", 0.02},
		{30, "30 fps", 0.01},
		{48, "48 fps", 0.01},
		{50, "50 fps", 0.01},
		{59.94, "59.94 fps", 0.02},
		{60, "60 fps", 0.01},
	}
	for _, k := range known {
		diff := fps - k.val
		if diff < 0 {
			diff = -diff
		}
		if diff < k.tol {
			return k.label
		}
	}
	return fmt.Sprintf("%.3g fps", fps)
}

func popupFormatDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func popupFormatBitrate(bps int64) string {
	if bps <= 0 {
		return ""
	}
	if bps >= 1_000_000 {
		return fmt.Sprintf("%.1f Mb/s", float64(bps)/1_000_000)
	}
	return fmt.Sprintf("%.0f kb/s", float64(bps)/1000)
}

func popupFormatAudio(m *meta.Meta) string {
	var parts []string
	if m.AudioCodec != "" {
		parts = append(parts, m.AudioCodec)
	}
	if m.AudioChannels > 0 {
		parts = append(parts, popupFormatChannels(m.AudioChannels))
	}
	if m.AudioSampleRate > 0 {
		parts = append(parts, fmt.Sprintf("%.0f kHz", float64(m.AudioSampleRate)/1000))
	}
	if m.AudioBitDepth > 0 {
		parts = append(parts, fmt.Sprintf("%d-bit", m.AudioBitDepth))
	}
	return strings.Join(parts, " ")
}

func popupFormatCodecs(m *meta.Meta) string {
	var parts []string
	if m.VideoCodec != "" {
		parts = append(parts, m.VideoCodec)
	}
	if m.AudioCodec != "" {
		parts = append(parts, m.AudioCodec)
	}
	return strings.Join(parts, " / ")
}

func popupFormatChannels(n int) string {
	switch n {
	case 1:
		return "Mono"
	case 2:
		return "Stereo"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	default:
		return fmt.Sprintf("%dch", n)
	}
}

// metaAsText formats a Meta as plain text suitable for clipboard.
func metaAsText(m *meta.Meta) string {
	var sb strings.Builder
	sb.WriteString("Path: " + m.Path + "\n")
	sb.WriteString("Kind: " + m.Kind + "\n")
	sb.WriteString("Size: " + popupFormatSize(m) + "\n")
	if !m.Modified.IsZero() {
		sb.WriteString("Modified: " + popupFormatDate(m.Modified) + "\n")
	}
	if m.VideoCodec != "" {
		sb.WriteString("Video: " + m.VideoCodec + "\n")
	}
	if m.AudioCodec != "" || m.AudioChannels > 0 {
		sb.WriteString("Audio: " + popupFormatAudio(m) + "\n")
	}
	if m.Duration > 0 {
		sb.WriteString("Duration: " + popupFormatDuration(m.Duration) + "\n")
	}
	if m.Width > 0 {
		sb.WriteString(fmt.Sprintf("Resolution: %d × %d\n", m.Width, m.Height))
	}
	if m.Framerate > 0 {
		sb.WriteString("Framerate: " + popupFormatFPS(m.Framerate) + "\n")
	}
	if m.Bitrate > 0 {
		sb.WriteString("Bitrate: " + popupFormatBitrate(m.Bitrate) + "\n")
	}
	if m.CameraMake != "" || m.CameraModel != "" {
		sb.WriteString("Camera: " + m.CameraMake + " / " + m.CameraModel + "\n")
	}
	if m.Lens != "" {
		sb.WriteString("Lens: " + m.Lens + "\n")
	}
	if m.Shutter != "" || m.Aperture != "" || m.ISO > 0 {
		sb.WriteString(fmt.Sprintf("Exposure: %s %s ISO %d\n", m.Shutter, m.Aperture, m.ISO))
	}
	return sb.String()
}
