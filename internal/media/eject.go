// Package media - eject.go handles diskutil eject operations.
package media

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ErrBusy is returned when a volume is busy and cannot be cleanly ejected.
var ErrBusy = errors.New("volume is busy")

// EjectResult holds the result of an eject operation.
type EjectResult struct {
	MountPoint string
	Success    bool
	Forced     bool
	Err        error
}

// EjectMsg is a Bubbletea message sent after an eject completes.
type EjectMsg struct {
	Result EjectResult
}

// EjectCmd returns a tea.Cmd that ejects the volume at mountPoint.
// It tries a clean eject first, then force eject if the volume is busy.
func EjectCmd(mountPoint string, force bool) tea.Cmd {
	return func() tea.Msg {
		if force {
			err := ForceEject(mountPoint)
			return EjectMsg{Result: EjectResult{
				MountPoint: mountPoint,
				Success:    err == nil,
				Forced:     true,
				Err:        err,
			}}
		}

		err := Eject(mountPoint)
		if errors.Is(err, ErrBusy) {
			return EjectMsg{Result: EjectResult{
				MountPoint: mountPoint,
				Success:    false,
				Forced:     false,
				Err:        err,
			}}
		}

		return EjectMsg{Result: EjectResult{
			MountPoint: mountPoint,
			Success:    err == nil,
			Forced:     false,
			Err:        err,
		}}
	}
}

// Eject attempts a clean unmount and eject using diskutil.
func Eject(mountPoint string) error {
	out, err := exec.Command("diskutil", "eject", mountPoint).CombinedOutput()
	if err != nil {
		stderr := string(out)
		if strings.Contains(strings.ToLower(stderr), "busy") ||
			strings.Contains(strings.ToLower(stderr), "couldn't unmount") {
			return ErrBusy
		}
		return fmt.Errorf("diskutil eject failed: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// ForceEject forcefully unmounts the disk. Use only after Eject returns ErrBusy.
func ForceEject(mountPoint string) error {
	out, err := exec.Command("diskutil", "unmountDisk", "force", mountPoint).CombinedOutput()
	if err != nil {
		return fmt.Errorf("diskutil force eject failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
