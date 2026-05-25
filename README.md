# Wrangler — Project Specification
### A filmmaker's data offload tool for macOS

---

## What this is

Wrangler is a terminal UI (TUI) tool for macOS that makes safe, verified file offloading accessible to independent filmmakers and DITs. It wraps `rsync` and `xxhash` in a well-designed, keyboard-driven interface.

The core problem it solves: copying camera card footage on macOS today means using Finder (no verification, no resume, no control) or paying $300+/year for ShotPut Pro. Wrangler is the free, open-source alternative that does the job correctly.

**The philosophy:** every copy is a transaction with a full lifecycle. Nothing is "done" until it is verified.

---

## Technical Stack

| Concern | Choice | Reason |
|---|---|---|
| Language | Go | Single static binary, ideal for curl/brew distribution |
| TUI framework | Bubbletea | Best-in-class Go TUI, component model, active community |
| Styling | Lipgloss | Pairs with Bubbletea, expressive layout and color |
| File hashing | xxHash (via `cespare/xxhash`) | Industry standard for fast verification, used by Hedge/ShotPut |
| File copying | `rsync` subprocess | Resumable, battle-tested, exposes progress over stdout |
| Media detection | `diskutil` + FSEvents (polling fallback) | macOS-native drive mount/unmount events |
| Eject | `diskutil eject` / `diskutil unmountDisk force` | Handles both clean and force eject |
| Build/release | GitHub Actions | Cross-compile darwin/amd64 + darwin/arm64 on every release tag |
| Distribution | Homebrew tap + curl install script | Standard for Go CLI tools |

---

## Core Concept: The Transaction

A **transaction** is the atomic unit of Wrangler. It represents one offload job.

```
Transaction {
  id          string          // short human-readable ID e.g. "WR-004"
  label       string          // auto-generated or user-set e.g. "A001_20240815"
  source      path            // single source directory (camera card)
  destinations []path         // 1 or 2 destination paths
  state       enum            // see lifecycle below
  progress    TransferProgress
  verify      VerifyResult    // populated after copy phase
  report      ReportMeta      // populated on completion
  created_at  time
  finished_at time
}
```

### Transaction Lifecycle

```
QUEUED → RUNNING → VERIFYING → DONE
                              → FAILED
         ↕
       PAUSED
         ↑
      CANCELLED
```

- **QUEUED** — staged, waiting for prior transaction to finish or for user to start
- **RUNNING** — rsync subprocess active, progress streaming
- **PAUSED** — SIGSTOP sent to rsync, transfer suspended, resumable
- **VERIFYING** — copy complete, xxHash pass running source vs destination
- **DONE** — verified clean, report written
- **FAILED** — rsync error, or verification mismatch — never silently succeeds
- **CANCELLED** — user-terminated, partial files cleaned up

---

## UI Layout

The interface has three panels. Navigation between panels is keyboard-driven.

```
┌─────────────────────────────────────────────────────────────────┐
│ WRANGLER                                          WR-004 ██████ │  ← header / active job summary
├──────────────┬──────────────────────────┬──────────────────────┤
│              │                          │                      │
│  FILE        │  TRANSACTION             │  TRANSACTION         │
│  BROWSER     │  DETAIL                  │  QUEUE               │
│              │                          │                      │
│  /Volumes    │  [active transaction]    │  WR-001  ✓ DONE      │
│  ├ CARD_A    │                          │  WR-002  ✓ DONE      │
│  ├ CARD_B    │  Source:                 │  WR-003  ↓ RUNNING   │
│  └ SSD_01    │    /Volumes/CARD_A       │  WR-004  ⏸ PAUSED   │
│              │  Destinations:           │  WR-005  ○ QUEUED    │
│  ~/Desktop   │    /Volumes/SSD_01       │                      │
│  ~/Movies    │    /Volumes/SSD_02       │                      │
│              │                          │                      │
│              │  ████████████░░░░  67%   │                      │
│              │  312 MB/s  ETA 4m 12s    │                      │
│              │  18,432 / 27,891 files   │                      │
│              │                          │                      │
│              │  [p] pause  [c] cancel   │                      │
│              │  [v] verify now          │                      │
│              │                          │                      │
└──────────────┴──────────────────────────┴──────────────────────┘
│ [tab] switch panel  [n] new transaction  [e] eject  [q] quit   │  ← keybindings footer
└─────────────────────────────────────────────────────────────────┘
```

### Panel: File Browser (left)
- Shows mounted volumes under `/Volumes` automatically
- Shows configurable bookmarks (home, Movies folder, common destinations)
- External drives and camera cards appear immediately on mount — no refresh needed
- Selecting a path sets it as source or destination for new transaction
- `[e]` on a selected volume triggers eject flow
- Visual indicator distinguishes camera cards from general drives (by filesystem type: exFAT, FAT32 = likely camera card)

### Panel: Transaction Detail (center)
- Shows the currently focused transaction in full
- During RUNNING: live progress bar, speed (MB/s), ETA, file count current/total
- During VERIFYING: separate progress bar for hash pass, files verified count
- During DONE: verification result, report location, total time, final file count + size
- During FAILED: clear error message, what failed, what to do next
- Keyboard shortcuts contextual to current state (pause/resume, cancel, open report)

### Panel: Transaction Queue (right)
- Scrollable list of all transactions in session
- Color-coded by state (see Visual Design below)
- Click or keyboard select to focus a transaction in the center panel
- Shows compact summary: ID, label, state icon, progress bar if active

---

## Visual Design Direction

Wrangler should feel like a **professional tool used on set** — not a toy, not a dev tool. Think the instrument cluster of a high-end camera. Dark, high-contrast, purposeful.

### Color Palette
```
Background:     #0D0D0D   (near black)
Surface:        #161616   (panel backgrounds)
Border:         #2A2A2A   (panel dividers)
Text primary:   #E8E8E8   (main content)
Text muted:     #666666   (labels, secondary info)
Text dim:       #3A3A3A   (inactive items)

Accent amber:   #F5A623   (active/running — the "filming" color)
Green:          #4CAF7D   (done/verified)
Red:            #E05252   (failed/error)
Blue:           #5B9BD5   (queued/ready)
White:          #FFFFFF   (paused state, neutral)
```

### Typography / Symbols
- Use Unicode box-drawing characters for layout (Bubbletea/Lipgloss handles this)
- Progress bars: `█░` style, amber when running, green when verified
- State icons: `✓` done, `↓` running, `⏸` paused, `○` queued, `✗` failed, `◌` verifying
- Filesystem tree uses `├` `└` `─` `│`
- Speed displayed as `312 MB/s`, size as `47.2 GB`, time as `4m 12s`

### Feel
- No rounded corners, no gradients — flat, sharp, utilitarian
- Information density is high but never cluttered
- Every state change is instant and visible — no ambiguity about what Wrangler is doing
- Error messages are direct and actionable, never vague

---

## Features: V1 Scope

### Must have
- [ ] File browser with `/Volumes` auto-detection
- [ ] External drive auto-appear on mount (polling or FSEvents)
- [ ] New transaction flow: select source → select destination(s) → confirm → queue
- [ ] Support 1 or 2 destinations per transaction
- [ ] rsync subprocess with `--progress` parsing
- [ ] Pause / Resume (SIGSTOP / SIGCONT)
- [ ] Cancel with partial file cleanup
- [ ] Post-copy xxHash verification (source vs each destination)
- [ ] Clear PASS / FAIL verification result
- [ ] Pre-copy space check — warn if destination insufficient
- [ ] Eject volume (`diskutil eject`) with confirmation
- [ ] Force eject (`diskutil unmountDisk force`) when clean eject fails
- [ ] Transaction queue, sequential execution
- [ ] Copy report per transaction (plain text file, written to destination): timestamp, source, destination(s), file count, total size, verification result, duration
- [ ] Keyboard navigation throughout, no mouse required
- [ ] Keybinding help footer always visible

### Explicitly out of V1 scope
- Simultaneous parallel transactions (sequential queue is fine for v1)
- Network / NAS destinations
- Camera-specific folder structure awareness (RED, ARRI, BRAW)
- PDF reports
- macOS notifications
- Presets / profiles
- Checksum format choice (xxHash only in v1)

---

## Distribution

### GitHub Actions release pipeline
On every `git tag vX.Y.Z` push, the CI workflow must:
1. Run `go test ./...`
2. Cross-compile for `darwin/amd64` and `darwin/arm64`
3. Package each as a `.tar.gz` with binary + README
4. Create a GitHub Release with both archives attached
5. Compute SHA256 of each archive (needed for Homebrew formula)

### Homebrew tap
A second repository `homebrew-wrangler` contains a Formula:
```ruby
class Wrangler < Formula
  desc "Filmmaker's data offload tool — safe, verified, controlled"
  homepage "https://github.com/YOUR_ORG/wrangler"
  version "0.1.0"
  # arch-specific urls and sha256s here
end
```

Install becomes:
```bash
brew tap YOUR_ORG/wrangler
brew install wrangler
```

### curl one-liner install script
`install.sh` hosted at repo root (or custom domain), detects arch, downloads correct binary:
```bash
curl -fsSL https://raw.githubusercontent.com/YOUR_ORG/wrangler/main/install.sh | sh
```

---

## Project Structure

```
wrangler/
├── main.go
├── go.mod
├── go.sum
├── README.md
├── install.sh
├── .github/
│   └── workflows/
│       └── release.yml
├── internal/
│   ├── ui/
│   │   ├── app.go          # root Bubbletea model
│   │   ├── browser.go      # file browser panel
│   │   ├── detail.go       # transaction detail panel
│   │   ├── queue.go        # transaction queue panel
│   │   ├── styles.go       # all Lipgloss styles / color palette
│   │   └── keys.go         # keybinding definitions
│   ├── transaction/
│   │   ├── transaction.go  # Transaction struct, state machine
│   │   ├── runner.go       # rsync subprocess, progress parsing, signals
│   │   └── verify.go       # xxHash verification pass
│   ├── media/
│   │   ├── watcher.go      # volume mount/unmount detection
│   │   └── eject.go        # diskutil eject / force eject
│   └── report/
│       └── report.go       # copy report writer
└── pkg/
    └── humanize/
        └── humanize.go     # MB/s, GB, duration formatting helpers
```

---

## Agentic Development Notes

When working with an AI coding agent on this project:

- **Always provide this spec** at the start of each session as context
- Build and test incrementally: get file browser working → add transaction model → add rsync runner → add verification → wire up UI
- The rsync `--progress` output format is well-documented — parse it carefully, it changes slightly between rsync versions; handle both
- SIGSTOP/SIGCONT work on the rsync process group, not just the process — use `cmd.SysProcAttr` with `Setpgid: true` and send signals to the process group
- Bubbletea is message-passing — rsync progress updates come in via a `tea.Cmd` that ticks and reads from a channel; don't block the update loop
- Test eject flows carefully — `diskutil` returns different exit codes for busy vs already-unmounted; handle both
- The GitHub Actions release workflow is boilerplate — generate it early so releases work from day one

---

## Success Criteria for V1

Wrangler v1 is done when a filmmaker can:

1. Plug in a camera card — it appears in the file browser immediately
2. Select it as source, select one or two drives as destinations
3. Start the transaction — see real progress, speed, ETA
4. Pause and resume mid-copy if needed
5. Watch the automatic verification pass complete
6. See a clear VERIFIED ✓ result (or a clear FAILED ✗ with detail)
7. Find a copy report sitting in the destination folder
8. Eject the card safely from within Wrangler
9. Install the whole tool with one command
