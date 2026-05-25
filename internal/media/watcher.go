// Package media handles volume detection and ejection for Wrangler.
package media

import (
	"os"
	"path/filepath"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// VolumeInfo describes a mounted volume.
type VolumeInfo struct {
	Name       string
	MountPoint string
	IsCamera   bool   // true if filesystem is exFAT or FAT32 (likely a camera card)
	FSType     string // e.g. "apfs", "exfat", "msdos"
	FreeBytes  int64
	TotalBytes int64
}

// VolumesChangedMsg is a Bubbletea message sent when the volume list changes.
type VolumesChangedMsg struct {
	Volumes []VolumeInfo
}

// Watcher polls /Volumes for mount/unmount events.
type Watcher struct {
	volumesCh chan []VolumeInfo
	stopCh    chan struct{}
	interval  time.Duration
	current   []VolumeInfo
}

// NewWatcher creates a new Watcher with the given poll interval.
func NewWatcher(interval time.Duration) *Watcher {
	return &Watcher{
		volumesCh: make(chan []VolumeInfo, 1),
		stopCh:    make(chan struct{}),
		interval:  interval,
	}
}

// Start begins polling for volume changes.
func (w *Watcher) Start() {
	go func() {
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()

		// Send initial state immediately.
		vols := w.scan()
		w.current = vols
		select {
		case w.volumesCh <- vols:
		default:
		}

		for {
			select {
			case <-w.stopCh:
				return
			case <-ticker.C:
				vols := w.scan()
				if !volumesEqual(w.current, vols) {
					w.current = vols
					select {
					case w.volumesCh <- vols:
					default:
						// Replace the pending message.
						select {
						case <-w.volumesCh:
						default:
						}
						select {
						case w.volumesCh <- vols:
						default:
						}
					}
				}
			}
		}
	}()
}

// Stop halts the polling goroutine.
func (w *Watcher) Stop() {
	close(w.stopCh)
}

// NextVolumeMsg returns a tea.Cmd that waits for the next volume change.
func (w *Watcher) NextVolumeMsg() tea.Cmd {
	return func() tea.Msg {
		select {
		case vols := <-w.volumesCh:
			return VolumesChangedMsg{Volumes: vols}
		case <-time.After(3 * time.Second):
			// Periodic check even without changes.
			return VolumesChangedMsg{Volumes: w.current}
		}
	}
}

// CurrentVolumes returns the most recently seen volume list.
func (w *Watcher) CurrentVolumes() []VolumeInfo {
	return w.current
}

// scan reads /Volumes and returns the current volume list.
func (w *Watcher) scan() []VolumeInfo {
	entries, err := os.ReadDir("/Volumes")
	if err != nil {
		return nil
	}

	var vols []VolumeInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		mp := filepath.Join("/Volumes", e.Name())

		var stat syscall.Statfs_t
		if err := syscall.Statfs(mp, &stat); err != nil {
			continue
		}

		// Skip the root volume (mounted at /).
		if byteSliceToString(stat.Mntonname[:]) == "/" {
			continue
		}

		fsType := fstypename(stat)
		isCamera := fsType == "exfat" || fsType == "msdos"
		free := int64(stat.Bavail) * int64(stat.Bsize)
		total := int64(stat.Blocks) * int64(stat.Bsize)

		vols = append(vols, VolumeInfo{
			Name:       e.Name(),
			MountPoint: mp,
			IsCamera:   isCamera,
			FSType:     fsType,
			FreeBytes:  free,
			TotalBytes: total,
		})
	}
	return vols
}

// fstypename extracts the filesystem type name from Statfs_t.Fstypename.
func fstypename(stat syscall.Statfs_t) string {
	b := make([]byte, 0, 16)
	for _, c := range stat.Fstypename {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}

// byteSliceToString converts a null-terminated byte array to string.
func byteSliceToString(b []int8) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		out = append(out, byte(c))
	}
	return string(out)
}

// volumesEqual returns true if two volume lists are identical by mount point.
func volumesEqual(a, b []VolumeInfo) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].MountPoint != b[i].MountPoint {
			return false
		}
	}
	return true
}
