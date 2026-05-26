// Package meta fetches file and folder metadata for the info popup.
package meta

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// FileType categorises the selected browser item.
type FileType int

const (
	FileTypeGeneric FileType = iota
	FileTypeFolder
	FileTypeVideo
	FileTypeImage
	FileTypeAudio
)

// Meta holds all metadata for a file or folder.
type Meta struct {
	Path string
	Type FileType

	// Common.
	Name        string
	Kind        string
	Permissions string
	Owner       string
	Volume      string
	Filesystem  string
	NotFound    bool

	// Sizes.
	Size       int64
	SizeOnDisk int64
	Bitrate    int64

	// Volume space (populated via syscall.Statfs — fast, works for drives).
	VolumeFree  int64
	VolumeTotal int64

	// Timestamps.
	Created  time.Time
	Modified time.Time

	// Directory stats (folder only).
	FileCount   int
	FolderCount int
	HiddenCount int

	// File metadata.
	Extension string

	// Codecs.
	VideoCodec string
	AudioCodec string
	ColorSpace string

	// Video dimensions / timing.
	Width     int
	Height    int
	Framerate float64
	Duration  time.Duration

	// Audio details.
	AudioChannels   int
	AudioSampleRate int
	AudioBitDepth   int

	// Camera / EXIF.
	CameraMake  string
	CameraModel string
	Lens        string
	Shutter     string
	Aperture    string
	FocalLength string
	ISO         int

	// Spotlight extras.
	WhereFrom     string
	Authors       string
	SpotlightNote string
}

// LoadedMsg is sent on the Bubbletea bus when metadata fetch completes.
type LoadedMsg struct {
	Path string
	M    *Meta
}

// SpinTickMsg drives the spinner animation while loading.
type SpinTickMsg struct {
	Path string
}

// FetchCmd returns a tea.Cmd that fetches metadata for path asynchronously.
func FetchCmd(path string) tea.Cmd {
	return func() tea.Msg {
		return LoadedMsg{Path: path, M: fetch(path)}
	}
}

// fetch is the synchronous metadata fetch.
func fetch(path string) *Meta {
	info, err := os.Lstat(path)
	if err != nil {
		return &Meta{Path: path, NotFound: true}
	}

	m := &Meta{
		Path:        path,
		Name:        info.Name(),
		Permissions: permString(info.Mode()),
		Modified:    info.ModTime(),
	}

	m.Volume, m.Filesystem = volumeInfo(path)

	// Volume free / total space via Statfs (fast, reliable for drives and folders).
	var stfs syscall.Statfs_t
	if syscall.Statfs(path, &stfs) == nil {
		bsize := int64(stfs.Bsize)
		m.VolumeTotal = int64(stfs.Blocks) * bsize
		m.VolumeFree = int64(stfs.Bavail) * bsize
	}

	if info.IsDir() {
		m.Type = FileTypeFolder
		m.Kind = "Folder"
		fetchFolder(m)
	} else {
		m.Size = info.Size()
		m.SizeOnDisk = duSize(path)
		ext := strings.ToLower(filepath.Ext(path))
		m.Extension = ext
		m.Type = detectType(ext)
		fetchFile(m)
	}

	// Spotlight enrichment.
	if fields := RunMdls(path); len(fields) > 0 {
		ApplyMdls(m, fields)
	}

	// ffprobe enrichment for media files.
	if (m.Type == FileTypeVideo || m.Type == FileTypeAudio) && ffprobeAvailable() {
		ApplyFfprobe(m, path)
	}

	return m
}

// fetchFolder counts items inside the directory.
func fetchFolder(m *Meta) {
	entries, err := os.ReadDir(m.Path)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			m.HiddenCount++
			continue
		}
		if e.IsDir() {
			m.FolderCount++
		} else {
			m.FileCount++
		}
	}
	m.SizeOnDisk = duSize(m.Path)
	m.Size = m.SizeOnDisk
}

// fetchFile sets Kind based on extension.
func fetchFile(m *Meta) {
	switch m.Type {
	case FileTypeVideo:
		m.Kind = "Video File"
	case FileTypeImage:
		if rawExts[m.Extension] {
			m.Kind = "RAW Image"
		} else {
			m.Kind = "Image File"
		}
	case FileTypeAudio:
		m.Kind = "Audio File"
	default:
		m.Kind = "Document"
	}
}

// ── File-type detection ───────────────────────────────────────────────────────

var videoExts = map[string]bool{
	".mp4": true, ".mov": true, ".mxf": true, ".avi": true, ".mkv": true,
	".r3d": true, ".braw": true, ".ari": true, ".mts": true, ".m2ts": true,
	".wmv": true,
}
var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".tiff": true, ".tif": true,
	".dng": true, ".arw": true, ".cr3": true, ".cr2": true, ".nef": true,
	".orf": true, ".rw2": true, ".heic": true, ".gif": true, ".bmp": true,
}
var audioExts = map[string]bool{
	".wav": true, ".aiff": true, ".aif": true, ".mp3": true, ".flac": true,
	".aac": true, ".m4a": true, ".ogg": true,
}
var rawExts = map[string]bool{
	".dng": true, ".arw": true, ".cr3": true, ".cr2": true, ".nef": true,
	".r3d": true, ".braw": true,
}

func detectType(ext string) FileType {
	switch {
	case videoExts[ext]:
		return FileTypeVideo
	case imageExts[ext]:
		return FileTypeImage
	case audioExts[ext]:
		return FileTypeAudio
	default:
		return FileTypeGeneric
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// permString converts an os.FileMode to a classic Unix permission string.
func permString(mode os.FileMode) string {
	var b [10]byte
	switch {
	case mode&os.ModeDir != 0:
		b[0] = 'd'
	case mode&os.ModeSymlink != 0:
		b[0] = 'l'
	default:
		b[0] = '-'
	}
	const rwx = "rwxrwxrwx"
	for i, c := range rwx {
		if mode&(1<<uint(8-i)) != 0 {
			b[i+1] = byte(c)
		} else {
			b[i+1] = '-'
		}
	}
	return string(b[:])
}

// duSize returns disk usage of path in bytes using "du -sk".
func duSize(path string) int64 {
	out, err := exec.Command("du", "-sk", path).Output()
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0
	}
	kb, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0
	}
	return kb * 1024
}

// ── Volume info ───────────────────────────────────────────────────────────────

var sysVolNameOnce sync.Once
var sysVolName string

// getSystemVolumeName fetches the macOS boot volume name (usually "Macintosh HD").
func getSystemVolumeName() string {
	sysVolNameOnce.Do(func() {
		out, err := exec.Command("diskutil", "info", "/").Output()
		if err != nil {
			sysVolName = "Macintosh HD"
			return
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "Volume Name:") {
				if parts := strings.SplitN(line, ":", 2); len(parts) == 2 {
					sysVolName = strings.TrimSpace(parts[1])
					return
				}
			}
		}
		sysVolName = "Macintosh HD"
	})
	return sysVolName
}

var volNameCache sync.Map // mountPoint → volumeName (string)

// volumeInfo returns the volume name and filesystem type for the given path.
func volumeInfo(path string) (volName, fsType string) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return "", ""
	}

	fsType = nullTermStr(st.Fstypename[:])
	mount := nullTermStr(st.Mntonname[:])

	if mount == "/" {
		return getSystemVolumeName(), fsType
	}

	if v, ok := volNameCache.Load(mount); ok {
		return v.(string), fsType
	}

	// Volume name = last path component of mount point under /Volumes.
	if strings.HasPrefix(mount, "/Volumes/") {
		volName = filepath.Base(mount)
	} else {
		volName = mount
	}
	volNameCache.Store(mount, volName)
	return
}

// nullTermStr converts a null-terminated []int8 to a Go string.
func nullTermStr(b []int8) string {
	buf := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		buf = append(buf, byte(c))
	}
	return string(buf)
}

// ── Breadcrumb ────────────────────────────────────────────────────────────────

// Breadcrumb converts a POSIX path to a human-readable breadcrumb string.
//
//	/Volumes/CARD_A/DCIM        → CARD_A • DCIM
//	/Users/john/Movies/clip.mp4 → Macintosh HD • Users • john • Movies • clip.mp4
func Breadcrumb(path string, maxWidth int) string {
	if path == "" {
		return ""
	}

	volName, _ := volumeInfo(path)
	if volName == "" {
		volName = "/"
	}

	// Compute the path relative to its mount point.
	rel := path
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) == nil {
		mount := nullTermStr(st.Mntonname[:])
		rel = strings.TrimPrefix(path, mount)
		rel = strings.TrimPrefix(rel, "/")
	}

	// Build parts: volume name + path components.
	parts := []string{volName}
	for _, seg := range strings.Split(rel, "/") {
		if seg != "" {
			parts = append(parts, seg)
		}
	}

	full := strings.Join(parts, " • ")
	if maxWidth <= 0 || len(full) <= maxWidth {
		return full
	}

	// Truncate from the left, keeping as many trailing parts as fit.
	for i := 1; i < len(parts); i++ {
		attempt := "… • " + strings.Join(parts[i:], " • ")
		if len(attempt) <= maxWidth {
			return attempt
		}
	}
	if maxWidth > 1 {
		return "…" + full[len(full)-(maxWidth-1):]
	}
	return full
}

// ── Formatting helpers ────────────────────────────────────────────────────────

// FormatShutter formats a shutter speed in seconds to a fraction string.
func FormatShutter(secs float64) string {
	if secs <= 0 {
		return ""
	}
	if secs >= 1 {
		return fmt.Sprintf("%.1fs", secs)
	}
	return fmt.Sprintf("1/%d", int(math.Round(1.0/secs)))
}

// FormatFocalLength formats focal length in mm.
func FormatFocalLength(mm float64) string {
	if mm <= 0 {
		return ""
	}
	if mm == math.Trunc(mm) {
		return fmt.Sprintf("%dmm", int(mm))
	}
	return fmt.Sprintf("%.1fmm", mm)
}
