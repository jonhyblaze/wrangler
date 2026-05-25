// Package transaction - runner.go manages the rsync subprocess lifecycle.
package transaction

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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

	// multi-destination tracking
	destIndex    int
	destCount    int
	prevBytes    int64 // bytes done in prior destinations
	totalBytes   int64 // pre-calculated total
	totalFiles   int64
	doneBytes    int64 // from current rsync run
	doneFiles    int64 // from current rsync run
}

// gnuRsyncRe matches GNU rsync --info=progress2 output.
// Example: "      1.23G  45%  123.45MB/s    0:04:32 (xfr#1, ir-chk=10/20)"
var gnuRsyncRe = regexp.MustCompile(
	`\s*([\d.,]+[KMGTP]?)\s+(\d+)%\s+([\d.]+\s*[KMGTP]?B/s)\s+(\d+:\d+:\d+)`,
)

// openRsyncRe matches openrsync / legacy rsync --progress per-file output.
// Example: "   1234567 100%  123.45kB/s  0:00:10"
var openRsyncRe = regexp.MustCompile(
	`([\d,]+)\s+(\d+)%\s+([\d.]+\s*[KMGTP]?B/s)\s+(\d+:\d+:\d+)`,
)

// NewRunner creates a new Runner for the given transaction.
func NewRunner(tx *Transaction) *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{
		tx:         tx,
		progressCh: make(chan TransferProgress, 20),
		doneCh:     make(chan error, 1),
		cancelCtx:  ctx,
		cancelFn:   cancel,
	}
}

// Start begins the offload: pre-calculates sizes, then runs rsync for each destination.
// This runs the actual work in a goroutine and returns immediately.
func (r *Runner) Start() {
	r.rsyncPath, r.isGNURsync = findRsync()
	r.destCount = len(r.tx.Destinations)

	go func() {
		// Pre-calculate source size.
		bytes, files, err := calculateSize(r.tx.Source)
		if err != nil {
			r.tx.SetError(fmt.Errorf("failed to calculate source size: %w", err))
			r.doneCh <- err
			return
		}
		r.totalBytes = bytes
		r.totalFiles = files

		// Check space on all destinations.
		for _, dest := range r.tx.Destinations {
			if err := checkSpace(dest, bytes); err != nil {
				r.tx.SetError(err)
				r.doneCh <- err
				return
			}
		}

		// Initial progress update.
		p := TransferProgress{
			BytesTotal: bytes,
			FilesTotal: files,
			StartedAt:  time.Now(),
		}
		r.tx.UpdateProgress(p)

		// Transition to running.
		if err := r.tx.SetState(StateRunning); err != nil {
			r.doneCh <- err
			return
		}

		// Run rsync for each destination sequentially.
		for i, dest := range r.tx.Destinations {
			r.mu.Lock()
			r.destIndex = i
			r.doneBytes = 0
			r.doneFiles = 0
			r.mu.Unlock()

			if err := r.runRsync(dest); err != nil {
				r.tx.SetError(fmt.Errorf("rsync to %s failed: %w", dest, err))
				r.doneCh <- err
				return
			}

			r.mu.Lock()
			r.prevBytes += r.doneBytes
			r.mu.Unlock()
		}

		// Transition to verifying.
		if err := r.tx.SetState(StateVerifying); err != nil {
			r.doneCh <- err
			return
		}
		r.doneCh <- nil
	}()
}

// runRsync executes rsync from source to one destination.
func (r *Runner) runRsync(dest string) error {
	select {
	case <-r.cancelCtx.Done():
		return fmt.Errorf("cancelled")
	default:
	}

	// Ensure destination exists.
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("create destination: %w", err)
	}

	args := r.buildArgs(dest)
	cmd := exec.CommandContext(r.cancelCtx, r.rsyncPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Capture both stdout and stderr.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start rsync: %w", err)
	}

	r.mu.Lock()
	r.cmd = cmd
	r.pgid = cmd.Process.Pid // with Setpgid=true, pgid == pid
	r.mu.Unlock()

	// Parse progress in a goroutine.
	go r.parseProgress(stdout)
	// Drain stderr to prevent blocking.
	var stderrBuf strings.Builder
	go func() { io.Copy(&stderrBuf, stderr) }()

	waitErr := cmd.Wait()

	if waitErr != nil {
		// If it was cancelled, return context error instead.
		if r.cancelCtx.Err() != nil {
			return r.cancelCtx.Err()
		}
		return fmt.Errorf("%w\nstderr: %s", waitErr, stderrBuf.String())
	}
	return nil
}

// buildArgs constructs the rsync argument list.
func (r *Runner) buildArgs(dest string) []string {
	src := r.tx.Source
	// Trailing slash: copy contents of directory, not the directory itself.
	if !strings.HasSuffix(src, "/") {
		src += "/"
	}

	args := []string{
		"--archive",
		"--human-readable",
		"--partial",
		"--partial-dir=.wrangler_partial",
	}

	if r.isGNURsync {
		args = append(args, "--info=progress2", "--no-inc-recursive")
	} else {
		args = append(args, "--progress")
	}

	args = append(args, src, dest)
	return args
}

// parseProgress reads rsync stdout and sends TransferProgress updates to progressCh.
func (r *Runner) parseProgress(rd io.Reader) {
	scanner := bufio.NewScanner(rd)
	// Custom split function: split on \r or \n.
	scanner.Split(splitOnCR)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var p *TransferProgress
		if r.isGNURsync {
			p = r.parseGNULine(line)
		} else {
			p = r.parseOpenRsyncLine(line)
		}

		if p != nil {
			select {
			case r.progressCh <- *p:
			default:
				// Drop if buffer full — UI will catch up.
			}
		}
	}
}

// parseGNULine parses a GNU rsync --info=progress2 progress line.
func (r *Runner) parseGNULine(line string) *TransferProgress {
	m := gnuRsyncRe.FindStringSubmatch(line)
	if m == nil {
		return nil
	}

	pct, _ := strconv.ParseFloat(m[2], 64)
	speedBPS := parseSpeedStr(m[3])

	r.mu.Lock()
	defer r.mu.Unlock()

	// Calculate bytes done from percentage of total.
	bytesDone := int64(pct/100.0*float64(r.totalBytes)) + r.prevBytes

	r.updateSpeed(bytesDone, speedBPS)

	now := time.Now()
	r.doneBytes = int64(pct / 100.0 * float64(r.totalBytes))

	eta := 0
	if speedBPS > 0 && r.totalBytes > bytesDone {
		eta = int(float64(r.totalBytes-bytesDone) / float64(speedBPS))
	}

	return &TransferProgress{
		BytesDone:   bytesDone,
		BytesTotal:  r.totalBytes,
		FilesTotal:  r.totalFiles,
		SpeedBPS:    r.rollingSpeed(),
		ETASecs:     eta,
		LastUpdated: now,
		StartedAt:   r.tx.Progress.StartedAt,
	}
}

// parseOpenRsyncLine parses a legacy rsync --progress per-file line.
func (r *Runner) parseOpenRsyncLine(line string) *TransferProgress {
	m := openRsyncRe.FindStringSubmatch(line)
	if m == nil {
		// Check if it's a filename line (no match on the progress pattern).
		return nil
	}

	bytesStr := strings.ReplaceAll(m[1], ",", "")
	fileBytes, _ := strconv.ParseInt(bytesStr, 10, 64)
	pct, _ := strconv.ParseFloat(m[2], 64)
	speedBPS := parseSpeedStr(m[3])

	r.mu.Lock()
	defer r.mu.Unlock()

	// For openrsync, accumulate files when they hit 100%.
	if pct == 100 {
		r.doneFiles++
		r.doneBytes += fileBytes
	}

	totalDone := r.doneBytes + r.prevBytes
	r.updateSpeed(totalDone, speedBPS)

	now := time.Now()
	eta := 0
	rollingSpd := r.rollingSpeed()
	if rollingSpd > 0 && r.totalBytes > totalDone {
		eta = int(float64(r.totalBytes-totalDone) / float64(rollingSpd))
	}

	return &TransferProgress{
		BytesDone:   totalDone,
		BytesTotal:  r.totalBytes,
		FilesDone:   r.doneFiles + (r.prevBytes / max64(1, r.totalBytes/max64(1, r.totalFiles))),
		FilesTotal:  r.totalFiles,
		SpeedBPS:    rollingSpd,
		ETASecs:     eta,
		LastUpdated: now,
		StartedAt:   r.tx.Progress.StartedAt,
	}
}

// updateSpeed adds a sample to the rolling speed buffer.
// Must be called with r.mu held.
func (r *Runner) updateSpeed(bytesDone, reportedBPS int64) {
	_ = reportedBPS // use our own rolling average
	now := time.Now()
	r.speedBuf[r.speedIdx] = speedSample{t: now, bytes: bytesDone}
	r.speedIdx = (r.speedIdx + 1) % len(r.speedBuf)
	if r.speedFilled < len(r.speedBuf) {
		r.speedFilled++
	}
}

// rollingSpeed returns the rolling average speed in bytes/sec.
// Must be called with r.mu held.
func (r *Runner) rollingSpeed() int64 {
	if r.speedFilled < 2 {
		return 0
	}
	// Find oldest sample.
	oldestIdx := r.speedIdx
	for i := 0; i < r.speedFilled-1; i++ {
		oldestIdx = (oldestIdx - 1 + len(r.speedBuf)) % len(r.speedBuf)
	}
	newest := r.speedBuf[(r.speedIdx-1+len(r.speedBuf))%len(r.speedBuf)]
	oldest := r.speedBuf[oldestIdx]

	elapsed := newest.t.Sub(oldest.t).Seconds()
	if elapsed < 0.01 {
		return 0
	}
	bytesDelta := newest.bytes - oldest.bytes
	if bytesDelta < 0 {
		bytesDelta = 0
	}
	return int64(math.Round(float64(bytesDelta) / elapsed))
}

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

// Cancel terminates the rsync process group and cleans up partial files.
func (r *Runner) Cancel() {
	r.cancelFn()

	r.mu.Lock()
	pgid := r.pgid
	dests := r.tx.Destinations
	r.mu.Unlock()

	if pgid != 0 {
		// SIGCONT first in case the process is stopped; SIGKILL cannot be delivered to stopped processes.
		_ = syscall.Kill(-pgid, syscall.SIGCONT)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}

	// Clean up partial files.
	for _, dest := range dests {
		partialDir := filepath.Join(dest, ".wrangler_partial")
		_ = os.RemoveAll(partialDir)
	}

	r.tx.SetStateForce(StateCancelled)
}

// NextProgressMsg returns a tea.Cmd that waits for the next progress update.
func (r *Runner) NextProgressMsg() tea.Cmd {
	return func() tea.Msg {
		select {
		case p, ok := <-r.progressCh:
			if !ok {
				return nil
			}
			r.tx.UpdateProgress(p)
			return ProgressMsg{TxID: r.tx.ID, Progress: p}
		case err := <-r.doneCh:
			return RunnerDoneMsg{TxID: r.tx.ID, Err: err}
		case <-time.After(200 * time.Millisecond):
			// Timeout: return a tick so UI can check for state changes.
			return ProgressMsg{TxID: r.tx.ID, Progress: r.tx.Snapshot().Progress}
		}
	}
}

// findRsync returns the path to rsync and whether it is GNU rsync.
func findRsync() (string, bool) {
	// Prefer Homebrew GNU rsync on Apple Silicon.
	candidates := []string{
		"/opt/homebrew/bin/rsync",
		"/usr/local/bin/rsync",
		"/usr/bin/rsync",
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
	// Fall back to PATH.
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
			return nil // skip inaccessible files
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

// checkSpace verifies the destination has enough free space.
func checkSpace(dest string, required int64) error {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dest, &stat); err != nil {
		return nil // can't check, proceed
	}
	free := int64(stat.Bavail) * int64(stat.Bsize)
	if free < required {
		return fmt.Errorf(
			"insufficient space on %s: need %d bytes, have %d bytes free",
			dest, required, free,
		)
	}
	return nil
}

// parseSpeedStr parses a speed string like "123.45MB/s" or "4.5 KB/s" into bytes/sec.
func parseSpeedStr(s string) int64 {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "")

	var multiplier float64 = 1
	switch {
	case strings.Contains(s, "GB/s"):
		multiplier = 1024 * 1024 * 1024
		s = strings.ReplaceAll(s, "GB/s", "")
	case strings.Contains(s, "MB/s"):
		multiplier = 1024 * 1024
		s = strings.ReplaceAll(s, "MB/s", "")
	case strings.Contains(s, "KB/s"), strings.Contains(s, "kB/s"):
		multiplier = 1024
		s = strings.ReplaceAll(strings.ReplaceAll(s, "KB/s", ""), "kB/s", "")
	case strings.Contains(s, "B/s"):
		multiplier = 1
		s = strings.ReplaceAll(s, "B/s", "")
	}

	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return int64(v * multiplier)
}

// splitOnCR is a bufio.SplitFunc that splits on \r or \n.
func splitOnCR(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i, b := range data {
		if b == '\r' || b == '\n' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
