// Package transaction - verify.go runs post-copy xxHash verification.
//
// Because runner.go copies without a trailing slash, rsync places:
//   - a source directory DCIM     → dest/DCIM/
//   - a source file    video.mp4  → dest/video.mp4
//
// In both cases the effective root to verify is filepath.Join(dest, basename(source)).
// This file implements that correctly.
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
	TxID    string
	Checked int64
	Total   int64
	Current string
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
	source := filepath.Clean(v.tx.Source)

	// Build hash map for source.
	sourceHashes, total, err := v.hashPath(ctx, source, source, 0, 0)
	if err != nil {
		return result, fmt.Errorf("hashing source: %w", err)
	}
	result.FilesTotal = total

	// Grand total for progress bar: source + all destinations.
	grandTotal := total * int64(len(v.tx.Destinations)+1)

	var checked int64 = total
	for _, dest := range v.tx.Destinations {
		// rsync (no trailing slash) copied source item into dest.
		// So the copy lives at dest/basename(source).
		effectiveDest := filepath.Join(dest, filepath.Base(source))

		if _, err := os.Stat(effectiveDest); err != nil {
			result.Mismatches = append(result.Mismatches,
				fmt.Sprintf("not found in %s: %s", dest, filepath.Base(source)))
			continue
		}

		destHashes, _, err := v.hashPath(ctx, effectiveDest, effectiveDest, checked, grandTotal)
		if err != nil {
			return result, fmt.Errorf("hashing destination %s: %w", effectiveDest, err)
		}
		checked += total

		// Compare: every source path must exist in dest with the same hash.
		for relPath, srcHash := range sourceHashes {
			destHash, exists := destHashes[relPath]
			if !exists {
				result.Mismatches = append(result.Mismatches,
					fmt.Sprintf("missing in %s: %s", effectiveDest, relPath))
			} else if srcHash != destHash {
				result.Mismatches = append(result.Mismatches,
					fmt.Sprintf("hash mismatch in %s: %s", effectiveDest, relPath))
			}
		}
	}

	result.FilesChecked = checked
	result.Passed = len(result.Mismatches) == 0
	return result, nil
}

// hashPath hashes a file or directory rooted at root.
// For a file, returns {"." → hash}. For a directory, returns relative-path → hash.
func (v *Verifier) hashPath(
	ctx context.Context,
	target string, // path to hash (file or dir)
	root string,   // base for computing relative paths
	startChecked int64,
	grandTotal int64,
) (map[string]uint64, int64, error) {
	hashes := make(map[string]uint64)
	var count int64

	info, err := os.Stat(target)
	if err != nil {
		return hashes, 0, err
	}

	if !info.IsDir() {
		// Single-file source.
		h, err := hashFile(target)
		if err != nil {
			return hashes, 0, err
		}
		hashes["."] = h
		count = 1
		select {
		case v.progressCh <- VerifyProgressMsg{
			TxID:    v.tx.ID,
			Checked: startChecked + 1,
			Total:   grandTotal,
			Current: info.Name(),
		}:
		default:
		}
		return hashes, count, nil
	}

	// Directory: walk it.
	err = filepath.WalkDir(target, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
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

		// Skip macOS clutter and our own artefacts.
		base := filepath.Base(relPath)
		if base == ".DS_Store" || base == ".wrangler_partial" ||
			len(base) > 2 && base[:2] == "._" {
			return nil
		}
		// Skip wrangler report files.
		if len(base) > 16 && base[:16] == "wrangler_report_" {
			return nil
		}

		h, err := hashFile(path)
		if err != nil {
			return nil // skip unreadable
		}

		hashes[relPath] = h
		count++

		select {
		case v.progressCh <- VerifyProgressMsg{
			TxID:    v.tx.ID,
			Checked: startChecked + count,
			Total:   grandTotal,
			Current: relPath,
		}:
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
	buf := make([]byte, 4*1024*1024) // 4 MB read buffer
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return 0, err
	}
	return h.Sum64(), nil
}
