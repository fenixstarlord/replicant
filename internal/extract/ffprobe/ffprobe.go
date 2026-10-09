// Package ffprobe extracts container and stream basics with ffprobe.
package ffprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fenixstarlord/replicant/internal/clips"
	"github.com/fenixstarlord/replicant/internal/extract"
	"github.com/fenixstarlord/replicant/internal/meta"
)

// Extractor runs ffprobe. Path may be empty to search PATH.
type Extractor struct {
	Path string
}

func (e *Extractor) Name() string  { return "ffprobe" }
func (e *Extractor) Priority() int { return extract.PriorityProbe }

func (e *Extractor) bin() string {
	if e.Path != "" {
		return e.Path
	}
	return "ffprobe"
}

func (e *Extractor) Available(ctx context.Context) (bool, string) {
	out, err := exec.CommandContext(ctx, e.bin(), "-version").Output()
	if err != nil {
		return false, ""
	}
	line, _, _ := strings.Cut(string(out), "\n")
	return true, strings.TrimPrefix(strings.TrimSpace(line), "ffprobe version ")
}

// skipExt lists formats ffprobe cannot read usefully.
var skipExt = map[string]bool{"ari": true, "arx": true, "r3d": true}

func (e *Extractor) Matches(c *clips.Clip) bool {
	if c.Kind == clips.KindARRIRAW || c.Kind == clips.KindR3D {
		return false
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(c.PrimaryFile()), "."))
	return ext != "" && !skipExt[ext]
}

// Output is the subset of ffprobe's JSON we map. The full output is kept
// verbatim in Result.Raw.
type Output struct {
	Format  Format   `json:"format"`
	Streams []Stream `json:"streams"`
}

type Format struct {
	FormatName string            `json:"format_name"`
	Duration   string            `json:"duration"`
	Tags       map[string]string `json:"tags"`
}

type Stream struct {
	CodecType         string            `json:"codec_type"`
	CodecName         string            `json:"codec_name"`
	CodecTagString    string            `json:"codec_tag_string"`
	Profile           string            `json:"profile"`
	Width             int64             `json:"width"`
	Height            int64             `json:"height"`
	PixFmt            string            `json:"pix_fmt"`
	BitsPerRawSample  string            `json:"bits_per_raw_sample"`
	BitsPerSample     int64             `json:"bits_per_sample"`
	AvgFrameRate      string            `json:"avg_frame_rate"`
	RFrameRate        string            `json:"r_frame_rate"`
	NbFrames          string            `json:"nb_frames"`
	Channels          int64             `json:"channels"`
	SampleRate        string            `json:"sample_rate"`
	ColorTransfer     string            `json:"color_transfer"`
	ColorPrimaries    string            `json:"color_primaries"`
	SampleAspectRatio string            `json:"sample_aspect_ratio"`
	Tags              map[string]string `json:"tags"`
}

func (e *Extractor) Extract(ctx context.Context, root string, c *clips.Clip) (*meta.Result, error) {
	abs := filepath.Join(root, filepath.FromSlash(c.PrimaryFile()))
	cmd := exec.CommandContext(ctx, e.bin(), "-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", abs)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("ffprobe: %s", firstLine(msg))
	}
	return Parse(out, c.PrimaryFile())
}

// Parse maps ffprobe JSON output for the named file into fields.
func Parse(raw []byte, file string) (*meta.Result, error) {
	var o Output
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, fmt.Errorf("ffprobe json: %w", err)
	}
	f := Map(&o, file)
	return &meta.Result{Fields: f, Raw: json.RawMessage(bytes.TrimSpace(raw))}, nil
}

// Map converts probe output into normalized fields.
func Map(o *Output, file string) meta.Fields {
	var f meta.Fields
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(file), "."))

	f.Container = meta.Str(container(o.Format.FormatName, ext))
	if d, err := strconv.ParseFloat(o.Format.Duration, 64); err == nil && d > 0 {
		f.DurationS = meta.Float(d)
	}
	tags := lowerKeys(o.Format.Tags)
	if tc := tags["timecode"]; tc != "" {
		f.TCStart = meta.Str(tc)
	}
	if t := parseTime(tags["creation_time"], tags["com.apple.quicktime.creationdate"], tags["date"]); t != nil {
		f.RecordedAt = t
	}
	f.Reel = meta.Str(tags["reel_name"])
	f.CameraMake = meta.Str(firstNonEmpty(tags["com.apple.quicktime.make"], tags["make"]))
	f.CameraModel = meta.Str(firstNonEmpty(tags["com.apple.quicktime.model"], tags["model"]))

	var video, audio *Stream
	hasVideo := false
	for i := range o.Streams {
		s := &o.Streams[i]
		switch s.CodecType {
		case "video":
			hasVideo = true
			if video == nil && (s.Width > 0 || s.CodecName != "") {
				video = s
			}
		case "audio":
			if audio == nil {
				audio = s
			}
		}
		st := lowerKeys(s.Tags)
		if f.TCStart == nil && st["timecode"] != "" {
			f.TCStart = meta.Str(st["timecode"])
		}
		if f.Reel == nil && st["reel_name"] != "" {
			f.Reel = meta.Str(st["reel_name"])
		}
	}

	if video != nil {
		f.Codec = meta.Str(codecName(video))
		f.CodecDetail = meta.Str(strings.TrimSpace(strings.Join(nonEmpty(video.Profile, video.PixFmt), " ")))
		if video.Width > 0 {
			f.Width = meta.Int(video.Width)
			f.Height = meta.Int(video.Height)
		}
		if fps := parseRate(video.AvgFrameRate); fps > 0 {
			f.FPS = meta.Float(fps)
		} else if fps := parseRate(video.RFrameRate); fps > 0 {
			f.FPS = meta.Float(fps)
		}
		if bd := bitDepth(video); bd > 0 {
			f.BitDepth = meta.Int(bd)
		}
		if n, err := strconv.ParseInt(video.NbFrames, 10, 64); err == nil && n > 0 {
			f.FrameCount = meta.Int(n)
		} else if f.FPS != nil && f.DurationS != nil {
			f.FrameCount = meta.Int(int64(*f.DurationS**f.FPS + 0.5))
		}
		if g := gamma(video.ColorTransfer, video.ColorPrimaries); g != "" {
			f.ColorGamma = meta.Str(g)
		}
		if sq := squeeze(video.SampleAspectRatio); sq > 0 && sq != 1 {
			f.Squeeze = meta.Float(sq)
		}
		if f.TCStart != nil && f.TCEnd == nil && f.FPS != nil && f.FrameCount != nil {
			if end, ok := AddFrames(*f.TCStart, *f.FrameCount, *f.FPS); ok {
				f.TCEnd = meta.Str(end)
			}
		}
	} else if audio != nil && !hasVideo {
		f.Codec = meta.Str(audio.CodecName)
	}
	if audio != nil {
		if audio.Channels > 0 {
			f.AudioChannels = meta.Int(audio.Channels)
		}
		if sr, err := strconv.ParseInt(audio.SampleRate, 10, 64); err == nil && sr > 0 {
			f.SampleRate = meta.Int(sr)
		}
		if bd := audioBitDepth(audio); bd > 0 {
			f.AudioBitDepth = meta.Int(bd)
		}
	}
	return f
}

func container(formatName, ext string) string {
	if strings.Contains(formatName, ",") || formatName == "" {
		return ext
	}
	return formatName
}

var tagCodecs = map[string]string{
	"brhq": "BRAW", "brlq": "BRAW", "brcq": "BRAW", "brc3": "BRAW", "brc5": "BRAW", "brc8": "BRAW", "brcc": "BRAW",
	"CRAW": "Canon RAW",
}

func codecName(s *Stream) string {
	if s.CodecName != "" {
		return s.CodecName
	}
	if c, ok := tagCodecs[s.CodecTagString]; ok {
		return c
	}
	if c, ok := tagCodecs[strings.ToLower(s.CodecTagString)]; ok {
		return c
	}
	return strings.TrimSpace(s.CodecTagString)
}

func bitDepth(s *Stream) int64 {
	if n, err := strconv.ParseInt(s.BitsPerRawSample, 10, 64); err == nil && n > 0 {
		return n
	}
	// yuv422p10le, gbrp12le, yuva444p16be ...
	pf := s.PixFmt
	for i := 0; i < len(pf); i++ {
		if pf[i] == 'p' && i+1 < len(pf) && pf[i+1] >= '0' && pf[i+1] <= '9' {
			j := i + 1
			for j < len(pf) && pf[j] >= '0' && pf[j] <= '9' {
				j++
			}
			n, _ := strconv.ParseInt(pf[i+1:j], 10, 64)
			return n
		}
	}
	if pf != "" {
		return 8
	}
	return 0
}

func audioBitDepth(s *Stream) int64 {
	if n, err := strconv.ParseInt(s.BitsPerRawSample, 10, 64); err == nil && n > 0 {
		return n
	}
	if s.BitsPerSample > 0 {
		return s.BitsPerSample
	}
	// pcm_s24le, pcm_s16be, pcm_f32le
	if strings.HasPrefix(s.CodecName, "pcm_") && len(s.CodecName) >= 7 {
		n, _ := strconv.ParseInt(strings.TrimRight(s.CodecName[5:7], "lbe"), 10, 64)
		return n
	}
	return 0
}

func gamma(transfer, primaries string) string {
	switch transfer {
	case "arib-std-b67":
		return "HLG"
	case "smpte2084":
		return "PQ"
	case "bt709":
		if primaries == "bt2020" {
			return "Rec.2020"
		}
		return "Rec.709"
	case "":
		return ""
	}
	return transfer
}

func squeeze(sar string) float64 {
	num, den, ok := strings.Cut(sar, ":")
	if !ok {
		return 0
	}
	n, err1 := strconv.ParseFloat(num, 64)
	d, err2 := strconv.ParseFloat(den, 64)
	if err1 != nil || err2 != nil || d == 0 {
		return 0
	}
	return n / d
}

func parseRate(r string) float64 {
	num, den, ok := strings.Cut(r, "/")
	if !ok {
		v, _ := strconv.ParseFloat(r, 64)
		return v
	}
	n, err1 := strconv.ParseFloat(num, 64)
	d, err2 := strconv.ParseFloat(den, 64)
	if err1 != nil || err2 != nil || d == 0 {
		return 0
	}
	return n / d
}

func parseTime(candidates ...string) *time.Time {
	layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000000Z", "2006-01-02 15:04:05", "2006-01-02"}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		for _, l := range layouts {
			if t, err := time.Parse(l, c); err == nil {
				return meta.Time(t.UTC())
			}
		}
	}
	return nil
}

// AddFrames advances a non-drop timecode by n frames at the given rate.
// Drop-frame timecodes (with ';') are left alone.
func AddFrames(tc string, n int64, fps float64) (string, bool) {
	if strings.Contains(tc, ";") {
		return "", false
	}
	parts := strings.Split(tc, ":")
	if len(parts) != 4 {
		return "", false
	}
	var v [4]int64
	for i, p := range parts {
		x, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return "", false
		}
		v[i] = x
	}
	rate := int64(fps + 0.5)
	if rate <= 0 {
		return "", false
	}
	total := ((v[0]*60+v[1])*60+v[2])*rate + v[3] + n
	total %= 24 * 3600 * rate
	fr := total % rate
	total /= rate
	s := total % 60
	total /= 60
	m := total % 60
	h := total / 60
	return fmt.Sprintf("%02d:%02d:%02d:%02d", h, m, s, fr), true
}

func lowerKeys(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[strings.ToLower(k)] = v
	}
	return out
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
