// Package humanize provides human-readable formatting helpers for Wrangler.
package humanize

import (
	"fmt"
	"strings"
	"time"
)

const (
	KB = int64(1024)
	MB = int64(1024 * 1024)
	GB = int64(1024 * 1024 * 1024)
	TB = int64(1024 * 1024 * 1024 * 1024)
)

// Bytes formats a byte count as a human-readable string (e.g. "47.2 GB").
func Bytes(n int64) string {
	switch {
	case n >= TB:
		return fmt.Sprintf("%.1f TB", float64(n)/float64(TB))
	case n >= GB:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(GB))
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(MB))
	case n >= KB:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(KB))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// Speed formats a bytes-per-second rate as a human-readable string (e.g. "312 MB/s").
func Speed(bps int64) string {
	switch {
	case bps >= GB:
		return fmt.Sprintf("%.1f GB/s", float64(bps)/float64(GB))
	case bps >= MB:
		return fmt.Sprintf("%.0f MB/s", float64(bps)/float64(MB))
	case bps >= KB:
		return fmt.Sprintf("%.0f KB/s", float64(bps)/float64(KB))
	default:
		return fmt.Sprintf("%d B/s", bps)
	}
}

// Duration formats a time.Duration as a human-readable string (e.g. "4m 12s").
func Duration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60

	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// ProgressBar builds a progress bar string of the given width.
// Returns a string like "████████░░░░" with filled chars and empty chars.
func ProgressBar(pct float64, width int) string {
	if width <= 0 {
		return ""
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	filled := int(pct * float64(width))
	if filled > width {
		filled = width
	}
	empty := width - filled
	return strings.Repeat("█", filled) + strings.Repeat("░", empty)
}

// ShortPath truncates a path to maxLen characters, preserving the middle with "...".
func ShortPath(path string, maxLen int) string {
	if len(path) <= maxLen {
		return path
	}
	if maxLen < 5 {
		return path[:maxLen]
	}
	half := (maxLen - 3) / 2
	return path[:half] + "..." + path[len(path)-half:]
}

// Comma formats an integer with comma separators (e.g. 18432 → "18,432").
func Comma(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}
