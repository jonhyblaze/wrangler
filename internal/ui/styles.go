// Package ui contains the Bubbletea TUI models and Lipgloss styles for Wrangler.
package ui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/jonhyblaze/wrangler/internal/transaction"
)

// Color palette — dark, high-contrast, professional.
const (
	ColorBG      = lipgloss.Color("#0D0D0D")
	ColorSurface = lipgloss.Color("#161616")
	ColorBorder  = lipgloss.Color("#2A2A2A")
	ColorText    = lipgloss.Color("#E8E8E8")
	ColorMuted   = lipgloss.Color("#666666")
	ColorDim     = lipgloss.Color("#3A3A3A")
	ColorAmber   = lipgloss.Color("#F5A623")
	ColorGreen   = lipgloss.Color("#4CAF7D")
	ColorRed     = lipgloss.Color("#E05252")
	ColorBlue    = lipgloss.Color("#5B9BD5")
	ColorWhite   = lipgloss.Color("#FFFFFF")

	// Space-badge colours for volume free-space annotations.
	ColorSpaceBgGreen = lipgloss.Color("#2D5A3D") // dark green background
	ColorSpaceBgAmber = lipgloss.Color("#5A4000") // dark amber background
	ColorSpaceBgRed   = lipgloss.Color("#5A1A1A") // dark red background
	ColorSpaceFg      = lipgloss.Color("#C8C8C8") // light neutral foreground
)

// PanelFrameWidth is the total horizontal overhead of a panel's frame:
// NormalBorder (1 col each side = 2) + Padding(0,1) (1 col each side = 2) = 4.
// app.go renders each panel with PanelStyle.Width(col-2) so the outer width
// equals its allocated column `col` (Lipgloss adds the 2 border cols outside
// the width); the usable text area is then col - PanelFrameWidth. Sub-models
// receive m.width == col, so they must draw no wider than m.width -
// PanelFrameWidth — anything wider soft-wraps, adds rows, and shifts borders.
const PanelFrameWidth = 4

// StateColor returns the Lipgloss color for a given transaction state.
func StateColor(s transaction.State) lipgloss.Color {
	switch s {
	case transaction.StateRunning, transaction.StateVerifying:
		return ColorAmber
	case transaction.StateDone:
		return ColorGreen
	case transaction.StateFailed:
		return ColorRed
	case transaction.StateQueued:
		return ColorBlue
	case transaction.StatePaused:
		return ColorWhite
	case transaction.StateCancelled:
		return ColorMuted
	default:
		return ColorText
	}
}

// Base styles.
var (
	BaseStyle = lipgloss.NewStyle().
			Background(ColorBG).
			Foreground(ColorText)

	MutedStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	DimStyle = lipgloss.NewStyle().
			Foreground(ColorDim)

	AmberStyle = lipgloss.NewStyle().
			Foreground(ColorAmber)

	GreenStyle = lipgloss.NewStyle().
			Foreground(ColorGreen)

	RedStyle = lipgloss.NewStyle().
			Foreground(ColorRed)

	BlueStyle = lipgloss.NewStyle().
			Foreground(ColorBlue)

	WhiteStyle = lipgloss.NewStyle().
			Foreground(ColorWhite)

	BoldStyle = lipgloss.NewStyle().
			Foreground(ColorText).
			Bold(true)
)

// Panel styles.
var (
	PanelStyle = lipgloss.NewStyle().
			Background(ColorSurface).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(ColorBorder).
			Padding(0, 1)

	ActivePanelStyle = lipgloss.NewStyle().
				Background(ColorSurface).
				BorderStyle(lipgloss.NormalBorder()).
				BorderForeground(ColorAmber).
				Padding(0, 1)

	HeaderStyle = lipgloss.NewStyle().
			Background(ColorSurface).
			Foreground(ColorText).
			Padding(0, 1).
			Bold(true)

	FooterStyle = lipgloss.NewStyle().
			Background(ColorSurface).
			Foreground(ColorMuted).
			Padding(0, 1)

	// Footer segment styles. Every visible piece of the footer carries the
	// surface background explicitly so the row renders as one solid bar. If a
	// segment omitted the background, the Render's trailing \e[0m reset would
	// clear it and leave the following text on the terminal's default (black)
	// background — the "black rectangle" / inconsistent-[tab] artefact.
	FooterKeyStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Background(ColorSurface)

	FooterDescStyle = lipgloss.NewStyle().
			Foreground(ColorText).
			Background(ColorSurface)

	FooterSepStyle = lipgloss.NewStyle().
			Background(ColorSurface)

	PanelTitleStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Bold(true)

	SelectedItemStyle = lipgloss.NewStyle().
				Background(ColorBorder).
				Foreground(ColorText)

	ActiveItemStyle = lipgloss.NewStyle().
			Background(ColorAmber).
			Foreground(ColorBG).
			Bold(true)
)

// renderStateText returns a styled state string.
func renderStateText(s transaction.State) string {
	icon := s.Icon()
	name := s.String()
	color := StateColor(s)
	return lipgloss.NewStyle().Foreground(color).Render(icon + " " + name)
}
