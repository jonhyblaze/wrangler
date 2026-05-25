// Package transaction defines the core Transaction type and its state machine.
package transaction

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// State represents the lifecycle state of a transaction.
type State int

const (
	StateQueued     State = iota // staged, waiting to run
	StateRunning                 // rsync active
	StatePaused                  // SIGSTOP sent, resumable
	StateVerifying               // copy complete, xxHash pass running
	StateDone                    // verified clean, report written
	StateFailed                  // rsync error or verification mismatch
	StateCancelled               // user-terminated, partial files cleaned
)

// String returns the display name for a state.
func (s State) String() string {
	switch s {
	case StateQueued:
		return "QUEUED"
	case StateRunning:
		return "RUNNING"
	case StatePaused:
		return "PAUSED"
	case StateVerifying:
		return "VERIFYING"
	case StateDone:
		return "DONE"
	case StateFailed:
		return "FAILED"
	case StateCancelled:
		return "CANCELLED"
	default:
		return "UNKNOWN"
	}
}

// Icon returns the Unicode symbol representing the state.
func (s State) Icon() string {
	switch s {
	case StateQueued:
		return "○"
	case StateRunning:
		return "↓"
	case StatePaused:
		return "⏸"
	case StateVerifying:
		return "◌"
	case StateDone:
		return "✓"
	case StateFailed:
		return "✗"
	case StateCancelled:
		return "⊘"
	default:
		return "?"
	}
}

// TransferProgress holds real-time progress data during a copy operation.
type TransferProgress struct {
	BytesDone   int64
	BytesTotal  int64
	FilesDone   int64
	FilesTotal  int64
	SpeedBPS    int64 // bytes per second, rolling average
	ETASecs     int
	CurrentFile string
	StartedAt   time.Time
	LastUpdated time.Time
}

// Percent returns the transfer completion as a 0.0–1.0 value.
func (p TransferProgress) Percent() float64 {
	if p.BytesTotal == 0 {
		return 0
	}
	v := float64(p.BytesDone) / float64(p.BytesTotal)
	if v > 1 {
		v = 1
	}
	return v
}

// VerifyResult holds the result of a post-copy xxHash verification.
type VerifyResult struct {
	FilesChecked int64
	FilesTotal   int64
	Mismatches   []string // relative paths of files that don't match
	Passed       bool
	Duration     time.Duration
}

// ReportMeta holds metadata about the written copy report.
type ReportMeta struct {
	Paths []string // one path per destination
}

// TxSnapshot is a lock-free snapshot of a Transaction's state, safe to copy and pass by value.
type TxSnapshot struct {
	ID           string
	Label        string
	Source       string
	Destinations []string
	State        State
	Progress     TransferProgress
	Verify       VerifyResult
	Report       ReportMeta
	CreatedAt    time.Time
	FinishedAt   time.Time
	Err          error
}

var txCounter int64

// Transaction is the atomic unit of a Wrangler offload job.
type Transaction struct {
	mu sync.RWMutex

	ID           string
	Label        string
	Source       string
	Destinations []string
	State        State
	Progress     TransferProgress
	Verify       VerifyResult
	Report       ReportMeta
	CreatedAt    time.Time
	FinishedAt   time.Time
	Err          error
}

// New creates a new Transaction with a unique ID.
func New(source string, destinations []string) *Transaction {
	n := atomic.AddInt64(&txCounter, 1)
	label := fmt.Sprintf("WR-%03d", n)
	return &Transaction{
		ID:           label,
		Label:        label,
		Source:       source,
		Destinations: destinations,
		State:        StateQueued,
		CreatedAt:    time.Now(),
	}
}

// Snapshot returns a lock-free snapshot of the transaction's current state.
func (t *Transaction) Snapshot() TxSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return TxSnapshot{
		ID:           t.ID,
		Label:        t.Label,
		Source:       t.Source,
		Destinations: t.Destinations,
		State:        t.State,
		Progress:     t.Progress,
		Verify:       t.Verify,
		Report:       t.Report,
		CreatedAt:    t.CreatedAt,
		FinishedAt:   t.FinishedAt,
		Err:          t.Err,
	}
}

// GetState returns the current state of the transaction.
func (t *Transaction) GetState() State {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.State
}

// SetState transitions the transaction to newState.
// Returns an error if the transition is not valid.
func (t *Transaction) SetState(newState State) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !isValidTransition(t.State, newState) {
		return fmt.Errorf("invalid transition from %s to %s", t.State, newState)
	}

	t.State = newState
	if newState == StateDone || newState == StateFailed || newState == StateCancelled {
		t.FinishedAt = time.Now()
	}
	return nil
}

// SetStateForce transitions to newState without checking validity.
// Use only in error-recovery paths.
func (t *Transaction) SetStateForce(newState State) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.State = newState
	if newState == StateDone || newState == StateFailed || newState == StateCancelled {
		t.FinishedAt = time.Now()
	}
}

// UpdateProgress atomically updates the progress fields.
func (t *Transaction) UpdateProgress(p TransferProgress) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Progress = p
}

// SetError records an error and forces the Failed state.
func (t *Transaction) SetError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Err = err
	t.State = StateFailed
	t.FinishedAt = time.Now()
}

// SetVerifyResult records the verification result.
func (t *Transaction) SetVerifyResult(r VerifyResult) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Verify = r
}

// SetReportPaths records the paths of the written copy reports.
func (t *Transaction) SetReportPaths(paths []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Report = ReportMeta{Paths: paths}
}

// isValidTransition checks whether the given state transition is legal.
func isValidTransition(from, to State) bool {
	switch from {
	case StateQueued:
		return to == StateRunning || to == StateCancelled
	case StateRunning:
		return to == StatePaused || to == StateVerifying || to == StateFailed || to == StateCancelled
	case StatePaused:
		return to == StateRunning || to == StateCancelled
	case StateVerifying:
		return to == StateDone || to == StateFailed
	case StateDone, StateFailed, StateCancelled:
		return false // terminal states
	}
	return false
}
