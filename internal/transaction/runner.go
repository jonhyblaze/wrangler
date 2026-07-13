// Package transaction - runner.go manages the rsync subprocess lifecycle.
//
// Progress tracking strategy:
//
//	Rsync stdout is fully pipe-buffered when not connected to a terminal —
//	we only receive output when the 64 KB pipe buffer fills, or rsync exits.
//	Parsing rsync's --progress output therefore gives unreliable real-time data.
//
//	Instead we poll the destination directory size every 500 ms.
//	This is version-agnostic, works for both openrsync and GNU rsync, and gives
//	smooth progress for files of any size.
package transaction

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ProgressMsg is a Bubbletea message carrying progress updates.
type ProgressMsg struct {
	TxID     string
	Progress TransferProgress
}

// RunnerDoneMsg signals that all rsync processes have completed.
type RunnerDoneMsg struct {
	TxID string
	Err  error
}

// speedSample holds a (time, bytes) pair for rolling average calculation.
type speedSample struct {
	t     time.Time
	bytes int64
}

// Runner manages the rsync subprocess for a single transaction.
type Runner struct {
	tx         *Transaction
	cmd        *exec.Cmd
	pgid       int
	progressCh chan TransferProgress
	doneCh     chan error
	cancelCtx  context.Context
	cancelFn   context.CancelFunc

	mu          sync.Mutex
	speedBuf    [5]speedSample
	speedIdx    int
	speedFilled int

	isGNURsync bool
	rsyncPath  string

	// Multi-destination state.
	prevBytes  int64 // bytes already done in completed destinations
	totalBytes int64 // source_size × destCount (for smooth overall progress bar)
	totalFiles int64

	// destPreexisting records, per destination, whether dest/basename(source)
	// already existed before this transfer started. On cancel we only delete
	// folders we created ourselves — never pre-existing user data.
	destPreexisting map[string]bool
}

// NewRunner creates a new Runner for the given transaction.
func NewRunner(tx *Transaction) *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{
		tx:         tx,
		progressCh:      make(chan TransferProgress, 20),
		doneCh:          make(chan error, 1),
		cancelCtx:       ctx,
		cancelFn:        cancel,
		destPreexisting: make(map[string]bool),
	}
}

// Start launches the offload goroutine.
func (r *Runner) Start() {
	r.rsyncPath, r.isGNURsync = findRsync()

	go func() {
		// Pre-calculate source size for progress bar and space check.
		srcBytes, srcFiles, err := calculateSize(r.tx.Source)
		if err != nil {
			r.tx.SetError(fmt.Errorf("failed to calculate source size: %w", err))
			r.doneCh <- err
			return
		}
		// Total = source size × dest count so the progress bar spans all destinations.
		r.totalBytes = srcBytes * int64(len(r.tx.Destinations))
		r.totalFiles = srcFiles

		// Space check on every destination.
		for _, dest := range r.tx.Destinations {
			if err := checkSpace(dest, srcBytes); err != nil {
				r.tx.SetError(err)
				r.doneCh <- err
				return
			}
		}

		// Record which destination folders already existed, so a later cancel
		// only removes folders this transfer created (never pre-existing data).
		srcName := filepath.Base(r.tx.Source)
		r.mu.Lock()
		for _, dest := range r.tx.Destinations {
			if _, statErr := os.Stat(filepath.Join(dest, srcName)); statErr == nil {
				r.destPreexisting[dest] = true
			}
		}
		r.mu.Unlock()

		// Publish initial progress (0 done).
		r.tx.UpdateProgress(TransferProgress{
			BytesTotal: r.totalBytes,
			FilesTotal: r.totalFiles,
			StartedAt:  time.Now(),
		})

		if err := r.tx.SetState(StateRunning); err != nil {
			r.doneCh <- err
			return
		}

		// Run rsync for each destination sequentially.
		for _, dest := range r.tx.Destinations {
			// Poll destination size while rsync writes, giving real-time progress.
			pollerDone := make(chan struct{})
			go r.pollDestProgress(dest, srcBytes, pollerDone)

			rsyncErr := r.runRsync(dest)
			close(pollerDone) // stop poller regardless of rsync result

			if rsyncErr != nil {
				// A cancelled context means the user aborted this transfer.
				// Cancel() owns the terminal state (StateCancelled) and cleanup —
				// don't overwrite it with StateFailed / a "context canceled" error.
				if r.cancelCtx.Err() != nil {
					r.doneCh <- rsyncErr
					return
				}
				r.tx.SetError(fmt.Errorf("rsync to %s failed: %w", dest, rsyncErr))
				r.doneCh <- rsyncErr
				return
			}

			// Advance the cumulative offset.
			r.mu.Lock()
			r.prevBytes += srcBytes
			r.mu.Unlock()
		}

		if err := r.tx.SetState(StateVerifying); err != nil {
			r.doneCh <- err
			return
		}
		r.doneCh <- nil
	}()
}

// pollDestProgress polls the destination directory every 500 ms and pushes
// TransferProgress updates into progressCh.
func (r *Runner) pollDestProgress(dest string, srcBytes int64, done <-chan struct{}) {
	// rsync (no trailing slash) puts the copy at dest/basename(source).
	effectiveDest := filepath.Join(dest, filepath.Base(r.tx.Source))
	partialDir := filepath.Join(dest, ".wrangler_partial")

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-r.cancelCtx.Done():
			return
		case <-ticker.C:
			// Sum completed files + in-progress partial files.
			doneBytes, doneFiles, _ := calculateSize(effectiveDest)
			partialBytes, _, _ := calculateSize(partialDir)

			r.mu.Lock()
			currentTotal := doneBytes + partialBytes + r.prevBytes
			r.updateSpeed(currentTotal)
			speed := r.rollingSpeed()
			r.mu.Unlock()

			eta := 0
			if speed > 0 && r.totalBytes > currentTotal {
				eta = int(float64(r.totalBytes-currentTotal) / float64(speed))
			}

			p := TransferProgress{
				BytesDone:   currentTotal,
				BytesTotal:  r.totalBytes,
				FilesDone:   doneFiles,
				FilesTotal:  r.totalFiles,
				SpeedBPS:    speed,
				ETASecs:     eta,
				StartedAt:   r.tx.Progress.StartedAt,
				LastUpdated: time.Now(),
			}

			r.tx.UpdateProgress(p)
			select {
			case r.progressCh <- p:
			default:
			}
		}
	}
}

// runRsync executes rsync from source to one destination.
// Stdout and stderr are captured for error reporting only (not parsed for progress).
func (r *Runner) runRsync(dest string) error {
	select {
	case <-r.cancelCtx.Done():
		return fmt.Errorf("cancelled")
	default:
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	// Pre-create the partial/temp directory so rsync can use it immediately.
	if err := os.MkdirAll(filepath.Join(dest, ".wrangler_partial"), 0o755); err != nil {
		return fmt.Errorf("create partial directory: %w", err)
	}

	args := r.buildArgs(dest)
	cmd := exec.CommandContext(r.cancelCtx, r.rsyncPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Capture combined output — used only for error messages.
	var outBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start rsync: %w", err)
	}

	r.mu.Lock()
	r.cmd = cmd
	r.pgid = cmd.Process.Pid // with Setpgid=true, pgid == pid
	r.mu.Unlock()

	waitErr := cmd.Wait()

	if waitErr != nil {
		if r.cancelCtx.Err() != nil {
			return r.cancelCtx.Err()
		}
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			switch exitErr.ExitCode() {
			case 23:
				// Partial transfer — usually macOS extended attributes.
				// File data was copied; xxHash verification will confirm.
				return nil
			case 24:
				// Source files vanished mid-copy (harmless for camera cards).
				return nil
			}
		}
		return describeRsyncError(waitErr, strings.TrimSpace(outBuf.String()))
	}
	return nil
}

// buildArgs constructs the rsync argument list.
func (r *Runner) buildArgs(dest string) []string {
	// No trailing slash: rsync copies the directory/file itself into dest,
	// preserving its name. With a trailing slash rsync copies only contents.
	src := filepath.Clean(r.tx.Source)

	// Use absolute paths for partial/temp dirs so rsync resolves them correctly
	// regardless of its working directory.
	partialPath := filepath.Join(dest, ".wrangler_partial")

	args := []string{
		"--archive",
		"--human-readable",
		"--partial",
		// Keep interrupted partial files in partialPath so they can be resumed.
		"--partial-dir=" + partialPath,
		// Write in-progress temp files to partialPath as well.
		// Without this, rsync writes its temp file as a hidden file in dest
		// (e.g. dest/.filename.XXXXXX), which is outside the directory we poll
		// for progress. Redirecting temp files here means calculateSize(partialPath)
		// captures live write progress for both single-file and directory sources.
		"--temp-dir=" + partialPath,
		// Cross-filesystem safety (exFAT → APFS): skip Unix metadata.
		"--no-perms",
		"--no-owner",
		"--no-group",
		// Skip macOS clutter that causes exit 23 on exFAT sources.
		"--exclude=.DS_Store",
		"--exclude=._*",
		"--exclude=.Spotlight-V100",
		"--exclude=.fseventsd",
		"--exclude=.Trashes",
	}

	// GNU rsync: protect paths with spaces, brackets, or other special chars.
	if r.isGNURsync {
		args = append(args, "--protect-args")
	}

	args = append(args, src, dest)
	return args
}

// describeRsyncError returns a human-friendly error for common rsync failures.
func describeRsyncError(rsyncErr error, output string) error {
	lower := strings.ToLower(output)

	switch {
	case strings.Contains(lower, "permission denied") || strings.Contains(lower, "operation not permitted"):
		// Detect macOS TCC-protected paths.
		protected := []string{"music", "photos", "desktop", "documents", "downloads"}
		for _, p := range protected {
			if strings.Contains(lower, p) {
				return fmt.Errorf(
					"macOS blocked access to a protected folder.\n"+
						"Grant Full Disk Access to your terminal:\n"+
						"System Settings › Privacy & Security › Full Disk Access\n\n"+
						"rsync error: %s",
					output,
				)
			}
		}
		return fmt.Errorf("permission denied:\n%s", output)

	case strings.Contains(lower, "no space left") || strings.Contains(lower, "disk full"):
		return fmt.Errorf("destination disk is full:\n%s", output)

	case strings.Contains(lower, "read-only file system"):
		return fmt.Errorf("destination is read-only:\n%s", output)

	case strings.Contains(lower, "file name too long"):
		return fmt.Errorf(
			"a filename is too long for the destination filesystem.\n"+
				"Some FAT32/exFAT volumes limit filenames to 255 bytes.\n\n"+
				"rsync error: %s",
			output,
		)

	case output != "":
		return fmt.Errorf("%w\n%s", rsyncErr, output)

	default:
		return rsyncErr
	}
}

// ── Speed tracking ────────────────────────────────────────────────────────────

// updateSpeed adds a sample to the rolling speed buffer. Must hold r.mu.
func (r *Runner) updateSpeed(bytesDone int64) {
	r.speedBuf[r.speedIdx] = speedSample{t: time.Now(), bytes: bytesDone}
	r.speedIdx = (r.speedIdx + 1) % len(r.speedBuf)
	if r.speedFilled < len(r.speedBuf) {
		r.speedFilled++
	}
}

// rollingSpeed returns the rolling average speed in bytes/sec. Must hold r.mu.
func (r *Runner) rollingSpeed() int64 {
	if r.speedFilled < 2 {
		return 0
	}
	oldestIdx := r.speedIdx
	for i := 0; i < r.speedFilled-1; i++ {
		oldestIdx = (oldestIdx - 1 + len(r.speedBuf)) % len(r.speedBuf)
	}
	newest := r.speedBuf[(r.speedIdx-1+len(r.speedBuf))%len(r.speedBuf)]
	oldest := r.speedBuf[oldestIdx]

	elapsed := newest.t.Sub(oldest.t).Seconds()
	if elapsed < 0.1 {
		return 0
	}
	delta := newest.bytes - oldest.bytes
	if delta < 0 {
		delta = 0
	}
	return int64(math.Round(float64(delta) / elapsed))
}

// ── Lifecycle controls ────────────────────────────────────────────────────────

// Pause sends SIGSTOP to the rsync process group.
func (r *Runner) Pause() error {
	r.mu.Lock()
	pgid := r.pgid
	r.mu.Unlock()
	if pgid == 0 {
		return fmt.Errorf("no active rsync process")
	}
	return syscall.Kill(-pgid, syscall.SIGSTOP)
}

// Resume sends SIGCONT to the rsync process group.
func (r *Runner) Resume() error {
	r.mu.Lock()
	pgid := r.pgid
	r.mu.Unlock()
	if pgid == 0 {
		return fmt.Errorf("no active rsync process")
	}
	return syscall.Kill(-pgid, syscall.SIGCONT)
}

// Cancel terminates the rsync process group and removes partial files.
func (r *Runner) Cancel() {
	r.cancelFn()

	r.mu.Lock()
	pgid := r.pgid
	dests := r.tx.Destinations
	preexisting := r.destPreexisting
	r.mu.Unlock()

	if pgid != 0 {
		// SIGCONT first: SIGKILL cannot reach a stopped (SIGSTOP'd) process.
		_ = syscall.Kill(-pgid, syscall.SIGCONT)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}

	// Clean up everything this transfer wrote so cancel leaves the destination
	// as it was: the in-progress temp/partial scratch dir, and the copied
	// source folder itself (dest/basename(source)) — otherwise an aborted copy
	// masquerades as a complete one.
	//
	// Only delete the copied folder when this transfer created it. If a folder
	// of the same name already existed before we started, leave it untouched —
	// we can't tell our partial writes from the user's pre-existing data.
	srcName := filepath.Base(r.tx.Source)
	for _, dest := range dests {
		_ = os.RemoveAll(filepath.Join(dest, ".wrangler_partial"))
		if !preexisting[dest] {
			_ = os.RemoveAll(filepath.Join(dest, srcName))
		}
	}

	r.tx.SetStateForce(StateCancelled)
}

// TxID returns the ID of the transaction this runner is processing.
// Used by the UI to distinguish active-runner messages from orphaned ones.
func (r *Runner) TxID() string {
	return r.tx.ID
}

// NextProgressMsg returns a tea.Cmd that delivers the next progress or done event.
func (r *Runner) NextProgressMsg() tea.Cmd {
	return func() tea.Msg {
		select {
		case p, ok := <-r.progressCh:
			if !ok {
				return nil
			}
			return ProgressMsg{TxID: r.tx.ID, Progress: p}
		case err := <-r.doneCh:
			return RunnerDoneMsg{TxID: r.tx.ID, Err: err}
		case <-time.After(300 * time.Millisecond):
			// Heartbeat: return current snapshot so UI stays alive.
			return ProgressMsg{TxID: r.tx.ID, Progress: r.tx.Snapshot().Progress}
		}
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// findRsync returns the path to rsync and whether it is GNU rsync v3+.
func findRsync() (string, bool) {
	candidates := []string{
		"/opt/homebrew/bin/rsync",  // Apple Silicon Homebrew
		"/usr/local/bin/rsync",     // Intel Homebrew
		"/usr/bin/rsync",           // macOS system (openrsync)
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			out, err := exec.Command(p, "--version").Output()
			if err == nil && strings.Contains(string(out), "rsync  version 3") {
				return p, true
			}
			return p, false
		}
	}
	if path, err := exec.LookPath("rsync"); err == nil {
		out, err := exec.Command(path, "--version").Output()
		if err == nil && strings.Contains(string(out), "rsync  version 3") {
			return path, true
		}
		return path, false
	}
	return "rsync", false
}

// calculateSize walks path and returns total byte count and file count.
func calculateSize(path string) (bytes int64, files int64, err error) {
	err = filepath.WalkDir(path, func(_ string, d os.DirEntry, e error) error {
		if e != nil {
			return nil // skip inaccessible
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err == nil {
				bytes += info.Size()
				files++
			}
		}
		return nil
	})
	return
}

// checkSpace verifies the destination filesystem has enough free space.
func checkSpace(dest string, required int64) error {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dest, &stat); err != nil {
		return nil // can't check; proceed optimistically
	}
	free := int64(stat.Bavail) * int64(stat.Bsize)
	if free < required {
		return fmt.Errorf(
			"not enough space on %s: need %s, have %s free",
			dest,
			humanBytes(required),
			humanBytes(free),
		)
	}
	return nil
}

// humanBytes formats bytes without importing the humanize package (avoids cycle).
func humanBytes(n int64) string {
	const GB = 1 << 30
	const MB = 1 << 20
	const KB = 1 << 10
	switch {
	case n >= GB:
		return fmt.Sprintf("%.1f GB", float64(n)/GB)
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/MB)
	case n >= KB:
		return fmt.Sprintf("%.1f KB", float64(n)/KB)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
