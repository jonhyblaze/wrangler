package humanize_test

import (
	"testing"
	"time"

	"github.com/jonhyblaze/wrangler/pkg/humanize"
)

func TestBytes(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{500, "500 B"},
		{1500, "1.5 KB"},
		{1_500_000, "1.4 MB"},
		{1_500_000_000, "1.4 GB"},
		{1_500_000_000_000, "1.4 TB"},
	}
	for _, tt := range tests {
		got := humanize.Bytes(tt.n)
		if got != tt.want {
			t.Errorf("Bytes(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestSpeed(t *testing.T) {
	tests := []struct {
		bps  int64
		want string
	}{
		{512, "512 B/s"},
		{2048, "2 KB/s"},
		{314_572_800, "300 MB/s"}, // 300 * 1024 * 1024
	}
	for _, tt := range tests {
		got := humanize.Speed(tt.bps)
		if got != tt.want {
			t.Errorf("Speed(%d) = %q, want %q", tt.bps, got, tt.want)
		}
	}
}

func TestDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{45 * time.Second, "45s"},
		{4*time.Minute + 12*time.Second, "4m 12s"},
		{1*time.Hour + 23*time.Minute, "1h 23m"},
	}
	for _, tt := range tests {
		got := humanize.Duration(tt.d)
		if got != tt.want {
			t.Errorf("Duration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestProgressBar(t *testing.T) {
	got := humanize.ProgressBar(0.5, 10)
	if got != "█████░░░░░" {
		t.Errorf("ProgressBar(0.5, 10) = %q, want %q", got, "█████░░░░░")
	}
	got = humanize.ProgressBar(1.0, 4)
	if got != "████" {
		t.Errorf("ProgressBar(1.0, 4) = %q, want %q", got, "████")
	}
	got = humanize.ProgressBar(0, 4)
	if got != "░░░░" {
		t.Errorf("ProgressBar(0, 4) = %q, want %q", got, "░░░░")
	}
}

func TestShortPath(t *testing.T) {
	long := "/Volumes/CARD_A/DCIM/CAMERA/FOLDER_01/file.MXF"
	got := humanize.ShortPath(long, 30)
	if len(got) > 30 {
		t.Errorf("ShortPath too long: %d > 30, got %q", len(got), got)
	}

	short := "/tmp/file.txt"
	if humanize.ShortPath(short, 30) != short {
		t.Errorf("ShortPath modified a short path")
	}
}

func TestComma(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{18432, "18,432"},
		{1_000_000, "1,000,000"},
	}
	for _, tt := range tests {
		got := humanize.Comma(tt.n)
		if got != tt.want {
			t.Errorf("Comma(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}
