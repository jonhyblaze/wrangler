// Wrangler — A filmmaker's data offload tool for macOS.
// Safe, verified, keyboard-driven file transfers with rsync + xxHash.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jonhyblaze/wrangler/internal/media"
	"github.com/jonhyblaze/wrangler/internal/ui"
)

// version is injected at build time via -ldflags.
var version = "dev"

func main() {
	versionFlag := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Println("wrangler", version)
		os.Exit(0)
	}

	// Start the volume watcher.
	watcher := media.NewWatcher(2 * time.Second)
	watcher.Start()
	defer watcher.Stop()

	// Build the TUI.
	model := ui.NewApp(watcher)
	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "wrangler: fatal:", err)
		os.Exit(1)
	}
}
