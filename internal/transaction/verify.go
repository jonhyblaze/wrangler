// Package transaction - verify.go runs post-copy xxHash verification.
package transaction

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/cespare/xxhash/v2"
	tea "github.com/charmbracelet/bubbletea"
)

// VerifyProgressMsg is a Bubbletea message carrying verification progress.
type VerifyProgressMsg struct {
	TxID     string
	Checked  int64
	Total    int64
	Current  string
}

// VerifyDoneMsg signals that verification has completed.
type VerifyDoneMsg struct {
	TxID   string
	Result VerifyResult
	Err    error
}

// Verifier manages the xxHash verification pass for a transaction.
type Verifier struct {
	tx         *Transaction
	progressCh chan VerifyProgressMsg
	resultCh   chan VerifyDoneMsg
}

// NewVerifier creates a new Verifier for the given transaction.
func NewVerifier(tx *Transaction) *Verifier {
	return &Verifier{
		tx:         tx,
		progressCh: make(chan VerifyProgressMsg, 20),
		resultCh:   make(chan VerifyDoneMsg, 1),
	}
}

// Start launches verification in a goroutine.
func (v *Verifier) Start(ctx context.Context) {
	go func() {
		start := time.Now()
		result, err := v.run(ctx)
		result.Duration = time.Since(start)

		if err == nil {
			result.Passed = len(result.Mismatches) == 0
			if result.Passed {
				v.tx.SetVerifyResult(result)
				_ = v.tx.SetState(StateDone)
			} else {
				v.tx.SetVerifyResult(result)
				v.tx.SetError(fmt.Errorf("verification failed: %d file(s) did not match", len(result.Mismatches)))
			}
		}

		v.resultCh <- VerifyDoneMsg{TxID: v.tx.ID, Result: result, Err: err}
	}()
}

// run performs the full hash comparison.
func (v *Verifier) run(ctx context.Context) (VerifyResult, error) {
	result := VerifyResult{}

	// Build hash map for source.
	sourceHashes, total, err := v.hashDirectory(ctx, v.tx.Source, 0, 0, nil)
	if err != nil {
		return result, fmt.Errorf("hashing source: %w", err)
	}
	result.FilesTotal = total

	// Count files across all destinations for accurate progress.
	grandTotal := total * int64(len(v.tx.Destinations)+1)

	// Compare each destination against source.
	var checked int64 = total
	for _, dest := range v.tx.Destinations {
		destHashes, _, err := v.hashDirectory(ctx, dest, checked, grandTotal, sourceHashes)
		if err != nil {
			return result, fmt.Errorf("hashing destination %s: %w", dest, err)
		}
		checked += total

		// Find mismatches.
		for relPath, srcHash := range sourceHashes {
			destHash, exists := destHashes[relPath]
			if !exists {
				result.Mismatches = append(result.Mismatches, fmt.Sprintf("missing in %s: %s", dest, relPath))
			} else if srcHash != destHash {
				result.Mismatches = append(result.Mismatches, fmt.Sprintf("hash mismatch in %s: %s", dest, relPath))
			}
		}
	}

	result.FilesChecked = checked
	result.Passed = len(result.Mismatches) == 0
	return result, nil
}

// hashDirectory walks a directory and returns a map of relative path → xxHash.
func (v *Verifier) hashDirectory(
	ctx context.Context,
	root string,
	startChecked int64,
	grandTotal int64,
	_ map[string]uint64, // unused, for future filtering
) (map[string]uint64, int64, error) {
	hashes := make(map[string]uint64)
	var count int64

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible
		}
		if d.IsDir() {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		// Skip our own partial/report files.
		if filepath.Base(relPath) == ".wrangler_partial" {
			return nil
		}

		h, err := hashFile(path)
		if err != nil {
			return nil // skip unreadable files
		}

		hashes[relPath] = h
		count++

		// Send progress.
		msg := VerifyProgressMsg{
			TxID:    v.tx.ID,
			Checked: startChecked + count,
			Total:   grandTotal,
			Current: relPath,
		}
		select {
		case v.progressCh <- msg:
		default:
		}

		return nil
	})

	return hashes, count, err
}

// NextVerifyMsg returns a tea.Cmd that waits for the next verification progress or result.
func (v *Verifier) NextVerifyMsg() tea.Cmd {
	return func() tea.Msg {
		select {
		case msg, ok := <-v.progressCh:
			if !ok {
				return nil
			}
			return msg
		case result := <-v.resultCh:
			return result
		case <-time.After(500 * time.Millisecond):
			return nil
		}
	}
}

// hashFile computes the xxHash-64 of a file.
func hashFile(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	h := xxhash.New()
	buf := make([]byte, 4*1024*1024) // 4MB buffer
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return 0, err
	}
	return h.Sum64(), nil
}
