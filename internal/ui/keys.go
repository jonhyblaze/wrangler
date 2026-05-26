// Package ui - keys.go defines keybindings for Wrangler.
package ui

import "github.com/charmbracelet/bubbles/key"

// KeyMap holds all keybinding definitions.
type KeyMap struct {
	Tab            key.Binding
	Quit           key.Binding
	NewTransaction key.Binding
	Eject          key.Binding
	// Browser navigation
	NavigateInto key.Binding // Enter / → — go into a directory
	NavigateUp   key.Binding // ← / Backspace — go to parent directory
	Select       key.Binding // Space — select highlighted dir as source/dest
	// List movement
	Up       key.Binding
	Down     key.Binding
	PageUp   key.Binding
	PageDown key.Binding
	// Quick-jump keys (browser)
	GotoHome    key.Binding
	GotoVolumes key.Binding
	// Transaction controls
	Pause      key.Binding
	Cancel     key.Binding
	Verify     key.Binding
	OpenReport key.Binding
	// Info popup
	Info key.Binding
	// Confirmations
	Confirm    key.Binding
	Back       key.Binding
	ForceEject key.Binding
}

// DefaultKeyMap returns the default keybindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("[tab]", "switch panel"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("[q]", "quit"),
		),
		NewTransaction: key.NewBinding(
			key.WithKeys("n"),
			key.WithHelp("[n]", "new transaction"),
		),
		Eject: key.NewBinding(
			key.WithKeys("e"),
			key.WithHelp("[e]", "eject"),
		),
		NavigateInto: key.NewBinding(
			key.WithKeys("enter", "right", "l"),
			key.WithHelp("[enter/→]", "open dir"),
		),
		NavigateUp: key.NewBinding(
			key.WithKeys("left", "backspace", "h"),
			key.WithHelp("[←/bksp]", "parent dir"),
		),
		Select: key.NewBinding(
			key.WithKeys(" "),
			key.WithHelp("[space]", "select"),
		),
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("[↑/k]", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("[↓/j]", "down"),
		),
		PageUp: key.NewBinding(
			key.WithKeys("pgup", "shift+up"),
			key.WithHelp("[pgup/shift+↑]", "page up"),
		),
		PageDown: key.NewBinding(
			key.WithKeys("pgdown", "shift+down"),
			key.WithHelp("[pgdn/shift+↓]", "page down"),
		),
		GotoHome: key.NewBinding(
			key.WithKeys("~"),
			key.WithHelp("[~]", "home dir"),
		),
		GotoVolumes: key.NewBinding(
			key.WithKeys("v"),
			key.WithHelp("[v]", "/Volumes"),
		),
		Pause: key.NewBinding(
			key.WithKeys("p"),
			key.WithHelp("[p]", "pause/resume"),
		),
		Cancel: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("[c]", "cancel"),
		),
		Verify: key.NewBinding(
			key.WithKeys("V"),
			key.WithHelp("[V]", "verify now"),
		),
		OpenReport: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("[r]", "open report"),
		),
		Info: key.NewBinding(
			key.WithKeys("i"),
			key.WithHelp("[i]", "info"),
		),
		Confirm: key.NewBinding(
			key.WithKeys("y"),
			key.WithHelp("[y]", "confirm"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("[esc]", "back/cancel"),
		),
		ForceEject: key.NewBinding(
			key.WithKeys("f"),
			key.WithHelp("[f]", "force eject"),
		),
	}
}

// Panel is the currently focused UI panel.
type Panel int

const (
	PanelBrowser Panel = iota
	PanelDetail
	PanelQueue
)

func (p Panel) String() string {
	switch p {
	case PanelBrowser:
		return "browser"
	case PanelDetail:
		return "detail"
	case PanelQueue:
		return "queue"
	default:
		return "unknown"
	}
}
