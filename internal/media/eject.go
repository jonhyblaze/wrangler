// Package media - eject.go handles diskutil eject operations.
//
// Smart eject strategy:
//  1. Try a clean "diskutil eject".
//  2. Parse stderr to find all dissenters (processes blocking unmount).
//  3. If every dissenter is a known macOS system process (Spotlight, Thumbnail
//     extensions, launchd children with PPID 1, etc.) → force-unmount automatically.
//     These hold no user data and macOS respawns them immediately.
//  4. If a real user application is listed → surface its name so the user
//     can decide whether to force.
package media

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ErrBusy is returned when a user application is holding the volume.
var ErrBusy = errors.New("volume is busy")

// EjectResult holds the outcome of an eject operation.
type EjectResult struct {
	MountPoint  string
	Success     bool
	Forced      bool   // true if diskutil unmountDisk force was used
	AutoForced  bool   // true if auto-forced because only system procs were blocking
	BusyProcess string // set when a user app is the dissenter (ErrBusy case)
	Err         error
}

// EjectMsg is a Bubbletea message delivered after eject completes.
type EjectMsg struct {
	Result EjectResult
}

// EjectCmd returns a tea.Cmd that performs a smart eject of mountPoint.
// If force=true it skips the clean attempt and goes straight to force-unmount.
func EjectCmd(mountPoint string, force bool) tea.Cmd {
	return func() tea.Msg {
		if force {
			err := forceUnmount(mountPoint)
			return EjectMsg{Result: EjectResult{
				MountPoint: mountPoint,
				Success:    err == nil,
				Forced:     true,
				Err:        err,
			}}
		}
		return EjectMsg{Result: smartEject(mountPoint)}
	}
}

// smartEject tries a clean eject and automatically falls back to force when
// the only dissenters are system-managed processes.
func smartEject(mountPoint string) EjectResult {
	out, err := exec.Command("diskutil", "eject", mountPoint).CombinedOutput()
	if err == nil {
		return EjectResult{MountPoint: mountPoint, Success: true}
	}

	stderr := string(out)
	dissenters := parseDisssenters(stderr)

	if len(dissenters) == 0 {
		// No specific dissenter identified — check for generic "couldn't unmount"
		// language and attempt a force, since this is usually a transient system hold.
		lower := strings.ToLower(stderr)
		if strings.Contains(lower, "couldn't unmount") ||
			strings.Contains(lower, "could not be unmounted") ||
			strings.Contains(lower, "busy") {
			if ferr := forceUnmount(mountPoint); ferr == nil {
				return EjectResult{
					MountPoint: mountPoint,
					Success:    true,
					Forced:     true,
					AutoForced: true,
				}
			}
		}
		return EjectResult{
			MountPoint: mountPoint,
			Success:    false,
			Err:        fmt.Errorf("diskutil eject failed: %s", strings.TrimSpace(stderr)),
		}
	}

	// Check whether every dissenter is a system process safe to override.
	allSystem := true
	var userProc string
	for _, d := range dissenters {
		if !d.isSystemProcess() {
			allSystem = false
			userProc = d.name
			break
		}
	}

	if allSystem {
		// All dissenters are macOS system processes — force is safe.
		if ferr := forceUnmount(mountPoint); ferr == nil {
			return EjectResult{
				MountPoint: mountPoint,
				Success:    true,
				Forced:     true,
				AutoForced: true,
			}
		}
	}

	// A real user application is blocking the volume.
	return EjectResult{
		MountPoint:  mountPoint,
		Success:     false,
		BusyProcess: userProc,
		Err:         ErrBusy,
	}
}

// forceUnmount runs "diskutil unmountDisk force <mountPoint>".
func forceUnmount(mountPoint string) error {
	out, err := exec.Command("diskutil", "unmountDisk", "force", mountPoint).CombinedOutput()
	if err != nil {
		return fmt.Errorf("force unmount failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// ── Dissenter parsing ─────────────────────────────────────────────────────────

// dissenter represents one process blocking an unmount.
type dissenter struct {
	pid  string
	name string // basename of the process path
	ppid string // parent PID; "1" means it's a direct launchd child
}

// dissentRe matches: "dissented by PID 8474 (/Applications/Foo.app/.../Bar)"
var dissentRe = regexp.MustCompile(
	`(?i)dissented by PID\s+(\d+)\s+\(([^)]+)\)`,
)

// ppidRe matches: "Dissenter parent PPID 1 (/sbin/launchd)"
var ppidRe = regexp.MustCompile(
	`(?i)dissenter parent PPID\s+(\d+)`,
)

// parseDisssenters extracts all dissenter entries from diskutil stderr output.
func parseDisssenters(stderr string) []dissenter {
	var out []dissenter
	lines := strings.Split(stderr, "\n")

	var pending *dissenter
	for _, line := range lines {
		if m := dissentRe.FindStringSubmatch(line); m != nil {
			if pending != nil {
				out = append(out, *pending)
			}
			pending = &dissenter{
				pid:  m[1],
				name: filepath.Base(m[2]),
			}
		} else if m := ppidRe.FindStringSubmatch(line); m != nil && pending != nil {
			pending.ppid = m[1]
		}
	}
	if pending != nil {
		out = append(out, *pending)
	}
	return out
}

// isSystemProcess returns true when this dissenter is a macOS system service
// safe to evict without user confirmation.
func (d dissenter) isSystemProcess() bool {
	// Direct launchd child (PPID 1) = OS-managed daemon or extension.
	if d.ppid == "1" {
		return true
	}

	// Known system process name fragments (case-insensitive).
	systemFragments := []string{
		"ThumbnailExtension",
		"QuickLookSatellite",
		"QuickLookUIService",
		"QuickLookWorker",
		"mds",              // Spotlight metadata server
		"mds_stores",       // Spotlight index store
		"fseventsd",        // FSEvents daemon
		"diskarbitrationd", // disk arbitration
		"storagekitd",
		"revisiond",
		"nsurlsessiond",
		"com.apple.",       // any Apple bundle fragment
		"CoreServices",
		"launchd",
	}
	nameLower := strings.ToLower(d.name)
	for _, frag := range systemFragments {
		if strings.Contains(nameLower, strings.ToLower(frag)) {
			return true
		}
	}
	return false
}
