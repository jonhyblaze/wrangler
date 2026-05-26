// Package meta - mdls.go wraps the macOS mdls command.
package meta

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// RunMdls runs mdls on path and returns the parsed key/value map.
// Returns nil if mdls is unavailable or fails.
func RunMdls(path string) map[string]string {
	out, err := exec.Command("mdls", path).Output()
	if err != nil {
		return nil
	}
	return parseMdls(string(out))
}

// parseMdls parses mdls output into a flat key→value map.
//
// Supported value forms:
//
//	kMDItemFoo = "value"
//	kMDItemFoo = (null)
//	kMDItemFoo = 42
//	kMDItemFoo = (          ← multiline array
//	    "item1",
//	    "item2",
//	)
func parseMdls(output string) map[string]string {
	result := make(map[string]string)
	lines := strings.Split(output, "\n")

	var currentKey string
	var inArray bool
	var arrayItems []string

	for _, line := range lines {
		if inArray {
			trimmed := strings.TrimSpace(line)
			if trimmed == ")" {
				inArray = false
				result[currentKey] = strings.Join(arrayItems, ", ")
				currentKey = ""
				arrayItems = nil
			} else {
				item := strings.Trim(trimmed, `",`)
				if item != "" {
					arrayItems = append(arrayItems, item)
				}
			}
			continue
		}

		idx := strings.Index(line, " = ")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+3:])

		if value == "(null)" || value == "" {
			continue
		}
		if value == "(" {
			inArray = true
			currentKey = key
			arrayItems = nil
			continue
		}

		// Strip surrounding quotes from string values.
		if strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) && len(value) >= 2 {
			value = value[1 : len(value)-1]
		}

		result[key] = value
	}

	return result
}

// ApplyMdls maps parsed mdls key/value pairs onto a Meta struct.
func ApplyMdls(m *Meta, fields map[string]string) {
	// Timestamps.
	if v, ok := fields["kMDItemFSCreationDate"]; ok {
		if t := parseMdlsDate(v); !t.IsZero() {
			m.Created = t
		}
	}
	if v, ok := fields["kMDItemFSContentChangeDate"]; ok {
		if t := parseMdlsDate(v); !t.IsZero() {
			m.Modified = t
		}
	}

	// Kind and owner.
	if v, ok := fields["kMDItemKind"]; ok && v != "" {
		m.Kind = v
	}
	if v, ok := fields["kMDItemFSOwnerUserName"]; ok {
		m.Owner = v
	}

	// Codecs — mdls may list several, separated by commas when joined from an array.
	if v, ok := fields["kMDItemCodecs"]; ok && v != "" {
		for _, c := range strings.Split(v, ", ") {
			c = strings.TrimSpace(c)
			cL := strings.ToLower(c)
			switch {
			case strings.Contains(cL, "h.264") || strings.Contains(cL, "avc") ||
				strings.Contains(cL, "h264"):
				m.VideoCodec = "H.264"
			case strings.Contains(cL, "h.265") || strings.Contains(cL, "hevc") ||
				strings.Contains(cL, "h265"):
				m.VideoCodec = "H.265"
			case strings.Contains(cL, "prores"):
				m.VideoCodec = c
			case strings.Contains(cL, "aac"):
				m.AudioCodec = "AAC"
			case strings.Contains(cL, "alac"):
				m.AudioCodec = "ALAC"
			case strings.Contains(cL, "mp3"):
				m.AudioCodec = "MP3"
			case strings.Contains(cL, "flac"):
				m.AudioCodec = "FLAC"
			case strings.Contains(cL, "pcm") || strings.Contains(cL, "lpcm"):
				m.AudioCodec = "PCM"
			}
		}
	}

	// Duration.
	if v, ok := fields["kMDItemDurationSeconds"]; ok {
		if secs, err := strconv.ParseFloat(v, 64); err == nil && secs > 0 {
			m.Duration = time.Duration(secs * float64(time.Second))
		}
	}

	// Dimensions.
	if v, ok := fields["kMDItemPixelWidth"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			m.Width = n
		}
	}
	if v, ok := fields["kMDItemPixelHeight"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			m.Height = n
		}
	}
	if v, ok := fields["kMDItemVideoFrameRate"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			m.Framerate = f
		}
	}

	// Bitrate and audio.
	if v, ok := fields["kMDItemTotalBitRate"]; ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			m.Bitrate = n
		}
	}
	if v, ok := fields["kMDItemAudioChannelCount"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			m.AudioChannels = n
		}
	}
	if v, ok := fields["kMDItemAudioSampleRate"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			m.AudioSampleRate = int(f)
		}
	}
	if v, ok := fields["kMDItemAudioBitsPerSample"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			m.AudioBitDepth = n
		}
	}

	// Camera / EXIF.
	if v, ok := fields["kMDItemAcquisitionMake"]; ok {
		m.CameraMake = v
	}
	if v, ok := fields["kMDItemAcquisitionModel"]; ok {
		m.CameraModel = v
	}
	if v, ok := fields["kMDItemLensModel"]; ok {
		m.Lens = v
	}
	if v, ok := fields["kMDItemExposureTimeSeconds"]; ok {
		if secs, err := strconv.ParseFloat(v, 64); err == nil {
			m.Shutter = FormatShutter(secs)
		}
	}
	if v, ok := fields["kMDItemFNumber"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			m.Aperture = fmt.Sprintf("f/%.1f", f)
		}
	}
	if v, ok := fields["kMDItemFocalLength"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			m.FocalLength = FormatFocalLength(f)
		}
	}
	if v, ok := fields["kMDItemISOSpeed"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			m.ISO = n
		}
	}

	// Spotlight extras.
	if v, ok := fields["kMDItemWhereFroms"]; ok {
		m.WhereFrom = v
	}
	if v, ok := fields["kMDItemAuthors"]; ok {
		m.Authors = v
	}
	if v, ok := fields["kMDItemComment"]; ok {
		m.SpotlightNote = v
	}
}

// parseMdlsDate parses a date string from mdls output.
// Typical format: "2023-12-14 09:22:00 +0000"
func parseMdlsDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04:05 +0000",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
