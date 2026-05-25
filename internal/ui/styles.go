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
)

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
