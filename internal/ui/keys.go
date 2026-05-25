// Package ui - keys.go defines keybindings for Wrangler.
package ui

import "github.com/charmbracelet/bubbles/key"

// KeyMap holds all keybinding definitions.
type KeyMap struct {
	Tab            key.Binding
	Quit           key.Binding
	NewTransaction key.Binding
	Eject          key.Binding
	Select         key.Binding
	Up             key.Binding
	Down           key.Binding
	Pause          key.Binding
	Cancel         key.Binding
	Verify         key.Binding
	OpenReport     key.Binding
	Confirm        key.Binding
	Back           key.Binding
	ForceEject     key.Binding
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
		Select: key.NewBinding(
			key.WithKeys("enter", " "),
			key.WithHelp("[enter]", "select"),
		),
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("[↑/k]", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("[↓/j]", "down"),
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
			key.WithKeys("v"),
			key.WithHelp("[v]", "verify now"),
		),
		OpenReport: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("[r]", "open report"),
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
