// Package meta - ffprobe.go enriches video/audio metadata via ffprobe.
package meta

import (
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

var fpOnce sync.Once
var fpPath string

// ffprobeAvailable returns true if ffprobe is on PATH.
func ffprobeAvailable() bool {
	fpOnce.Do(func() {
		path, err := exec.LookPath("ffprobe")
		if err == nil {
			fpPath = path
		}
	})
	return fpPath != ""
}

// ── JSON structures ───────────────────────────────────────────────────────────

type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
	Format  ffprobeFormat   `json:"format"`
}

type ffprobeStream struct {
	CodecType      string            `json:"codec_type"`
	CodecName      string            `json:"codec_name"`
	Profile        string            `json:"profile"`
	Width          int               `json:"width"`
	Height         int               `json:"height"`
	RFrameRate     string            `json:"r_frame_rate"`
	AvgFrameRate   string            `json:"avg_frame_rate"`
	ChannelLayout  string            `json:"channel_layout"`
	Channels       int               `json:"channels"`
	SampleRate     string            `json:"sample_rate"`
	BitsPerSample  int               `json:"bits_per_sample"`
	ColorSpace     string            `json:"color_space"`
	BitRate        string            `json:"bit_rate"`
	Tags           map[string]string `json:"tags"`
}

type ffprobeFormat struct {
	Duration string            `json:"duration"`
	BitRate  string            `json:"bit_rate"`
	Tags     map[string]string `json:"tags"`
}

// ── Public API ────────────────────────────────────────────────────────────────

// ApplyFfprobe runs ffprobe on path and enriches m with audio/video metadata.
func ApplyFfprobe(m *Meta, path string) {
	if fpPath == "" {
		return
	}
	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_streams", "-show_format",
		path,
	}
	out, err := exec.Command(fpPath, args...).Output()
	if err != nil {
		return
	}

	var fp ffprobeOutput
	if err := json.Unmarshal(out, &fp); err != nil {
		return
	}

	// Duration from format.
	if fp.Format.Duration != "" {
		if secs, err := strconv.ParseFloat(fp.Format.Duration, 64); err == nil && secs > 0 {
			m.Duration = time.Duration(secs * float64(time.Second))
		}
	}

	// Bitrate from format (fallback if mdls didn't supply one).
	if fp.Format.BitRate != "" && m.Bitrate == 0 {
		if bps, err := strconv.ParseInt(fp.Format.BitRate, 10, 64); err == nil {
			m.Bitrate = bps
		}
	}

	for _, s := range fp.Streams {
		switch s.CodecType {
		case "video":
			if m.VideoCodec == "" {
				m.VideoCodec = normalizeCodec(s.CodecName, s.Profile)
			}
			if m.Width == 0 && s.Width > 0 {
				m.Width = s.Width
			}
			if m.Height == 0 && s.Height > 0 {
				m.Height = s.Height
			}
			if m.Framerate == 0 {
				m.Framerate = parseFrameRate(s.AvgFrameRate)
				if m.Framerate == 0 {
					m.Framerate = parseFrameRate(s.RFrameRate)
				}
			}
			if s.ColorSpace != "" && m.ColorSpace == "" {
				m.ColorSpace = s.ColorSpace
			}

		case "audio":
			if m.AudioCodec == "" {
				m.AudioCodec = normalizeCodec(s.CodecName, "")
			}
			if m.AudioChannels == 0 {
				m.AudioChannels = s.Channels
			}
			if m.AudioSampleRate == 0 && s.SampleRate != "" {
				if hz, err := strconv.Atoi(s.SampleRate); err == nil {
					m.AudioSampleRate = hz
				}
			}
			if m.AudioBitDepth == 0 && s.BitsPerSample > 0 {
				m.AudioBitDepth = s.BitsPerSample
			}
		}
	}
}

// normalizeCodec maps raw ffprobe codec names to human-readable strings.
func normalizeCodec(codec, profile string) string {
	switch strings.ToLower(codec) {
	case "h264", "avc":
		return "H.264"
	case "hevc", "h265":
		return "H.265"
	case "av1":
		return "AV1"
	case "vp9":
		return "VP9"
	case "vp8":
		return "VP8"
	case "prores":
		if profile != "" {
			return "ProRes " + profile
		}
		return "ProRes"
	case "dnxhd", "dnxhr":
		return "DNxHD"
	case "mjpeg":
		return "MJPEG"
	case "mpeg4":
		return "MPEG-4"
	case "mpeg2video":
		return "MPEG-2"
	case "aac":
		return "AAC"
	case "mp3":
		return "MP3"
	case "ac3":
		return "AC3"
	case "eac3":
		return "EAC3"
	case "flac":
		return "FLAC"
	case "alac":
		return "ALAC"
	case "opus":
		return "Opus"
	case "vorbis":
		return "Vorbis"
	case "pcm_s16le", "pcm_s24le", "pcm_s32le", "pcm_f32le", "pcm_s16be", "pcm_s24be":
		return "PCM"
	case "dts":
		return "DTS"
	default:
		if len(codec) == 0 {
			return ""
		}
		return strings.ToUpper(codec[:1]) + codec[1:]
	}
}

// parseFrameRate converts an ffprobe fractional frame rate to float64.
// "24000/1001" → 23.976004…, "25/1" → 25.0, "0/0" → 0.0
func parseFrameRate(r string) float64 {
	if r == "" {
		return 0
	}
	parts := strings.SplitN(r, "/", 2)
	if len(parts) != 2 {
		f, _ := strconv.ParseFloat(r, 64)
		return f
	}
	num, err1 := strconv.ParseFloat(parts[0], 64)
	den, err2 := strconv.ParseFloat(parts[1], 64)
	if err1 != nil || err2 != nil || den == 0 {
		return 0
	}
	return num / den
}
