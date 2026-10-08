package store

import (
	"encoding/csv"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/fenixstarlord/indexserver/internal/meta"
)

// csvHeader lists the columns of a clip CSV export.
var csvHeader = []string{
	"drive", "label", "location", "path", "clip", "kind", "files", "size_bytes", "modified",
	"clip_name", "reel", "camera_index", "scene", "take", "circled", "recorded_at", "tc_start", "tc_end", "duration_s", "frame_count",
	"container", "codec", "codec_detail", "width", "height", "sensor_mode", "fps", "capture_fps", "bit_depth", "color_gamma", "squeeze",
	"camera_make", "camera_model", "camera_serial", "firmware", "iso", "wb_kelvin", "tint", "shutter_angle", "shutter_speed", "nd",
	"lens", "focal_mm", "t_stop", "focus_distance", "audio_channels", "sample_rate", "audio_bit_depth", "look", "scan_id", "scanned_at",
}

func s(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func i(p *int64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatInt(*p, 10)
}
func f(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}
func b(p *bool) string {
	if p == nil {
		return ""
	}
	if *p {
		return "yes"
	}
	return "no"
}
func t(p *time.Time) string {
	if p == nil {
		return ""
	}
	return p.UTC().Format(time.RFC3339)
}

// WriteCSV writes clips as CSV.
func WriteCSV(w io.Writer, clips []ClipRow) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(csvHeader); err != nil {
		return err
	}
	for _, c := range clips {
		m := c.Meta
		rec := []string{
			c.DriveName, c.DriveLabel, c.Location, c.RootPath, c.Name, c.Kind, strconv.Itoa(c.FileCount), strconv.FormatInt(c.TotalSize, 10), c.ModTime.UTC().Format(time.RFC3339),
			s(m.ClipName), s(m.Reel), s(m.CameraIndex), s(m.Scene), s(m.Take), b(m.Circled), t(m.RecordedAt), s(m.TCStart), s(m.TCEnd), f(m.DurationS), i(m.FrameCount),
			s(m.Container), s(m.Codec), s(m.CodecDetail), i(m.Width), i(m.Height), s(m.SensorMode), f(m.FPS), f(m.CaptureFPS), i(m.BitDepth), s(m.ColorGamma), f(m.Squeeze),
			s(m.CameraMake), s(m.CameraModel), s(m.CameraSerial), s(m.Firmware), i(m.ISO), i(m.WBKelvin), f(m.Tint), f(m.ShutterAngle), s(m.ShutterSpeed), s(m.ND),
			s(m.Lens), f(m.FocalMM), f(m.TStop), s(m.FocusDistance), i(m.AudioChannels), i(m.SampleRate), i(m.AudioBitDepth), s(m.Look),
			strconv.FormatInt(c.ScanID, 10), c.ScannedAt.UTC().Format(time.RFC3339),
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// aleColumns are the ALE columns written, in order. Names follow Avid and
// ARRI conventions so Avid and Resolve import them directly.
var aleColumns = []string{
	"Name", "Tape", "Source File", "Start", "End", "Duration", "FPS", "Tracks",
	"Scene", "Take", "Circled", "Camera_index", "Reel_name", "Shoot_date",
	"Camera_model", "Camera_sn", "Original_video", "Frame_width", "Frame_height",
	"Exposure_index", "White_balance", "Cc_shift", "Shutter_angle", "Sensor_fps", "Gamma",
	"Lens_type", "Focal_length", "Iris", "Nd_filterdensity", "Look_name",
	"Audio_sr", "Audio_bit", "Drive", "Location", "Path",
}

// WriteALE writes clips as an Avid Log Exchange file. The heading FPS is
// the most common fps among the clips, or 24.
func WriteALE(w io.Writer, clips []ClipRow) error {
	fpsCount := map[string]int{}
	for _, c := range clips {
		if c.Meta.FPS != nil {
			fpsCount[fpsString(*c.Meta.FPS)]++
		}
	}
	headFPS, best := "24", 0
	for k, n := range fpsCount {
		if n > best {
			headFPS, best = k, n
		}
	}
	var sb strings.Builder
	sb.WriteString("Heading\r\nFIELD_DELIM\tTABS\r\nVIDEO_FORMAT\tCUSTOM\r\nAUDIO_FORMAT\t48kHz\r\nFPS\t" + headFPS + "\r\n\r\nColumn\r\n")
	sb.WriteString(strings.Join(aleColumns, "\t") + "\r\n\r\nData\r\n")
	for _, c := range clips {
		m := c.Meta
		name := s(m.ClipName)
		if name == "" {
			name = c.Name
		}
		src := c.RootPath
		if len(c.Files) > 0 {
			src = c.Files[0]
		}
		tracks := "V"
		if m.AudioChannels != nil {
			for n := int64(1); n <= *m.AudioChannels && n <= 8; n++ {
				tracks += "A" + strconv.FormatInt(n, 10)
			}
		}
		dur := ""
		if m.DurationS != nil && m.FPS != nil {
			dur = framesToTC(int64(*m.DurationS**m.FPS+0.5), *m.FPS)
		}
		date := ""
		if m.RecordedAt != nil {
			date = m.RecordedAt.Format("20060102")
		}
		video := s(m.Codec)
		if m.SensorMode != nil {
			video += " (" + *m.SensorMode + ")"
		}
		rec := []string{
			name, s(m.Reel), path.Base(src), s(m.TCStart), s(m.TCEnd), dur, f(m.FPS), tracks,
			s(m.Scene), s(m.Take), aleYesNo(m.Circled), s(m.CameraIndex), s(m.Reel), date,
			s(m.CameraModel), s(m.CameraSerial), video, i(m.Width), i(m.Height),
			i(m.ISO), i(m.WBKelvin), f(m.Tint), f(m.ShutterAngle), f(m.CaptureFPS), s(m.ColorGamma),
			s(m.Lens), f(m.FocalMM), f(m.TStop), s(m.ND), s(m.Look),
			i(m.SampleRate), i(m.AudioBitDepth), c.DriveName, c.Location, c.RootPath,
		}
		for j := range rec {
			rec[j] = strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(rec[j])
		}
		sb.WriteString(strings.Join(rec, "\t") + "\r\n")
	}
	_, err := io.WriteString(w, sb.String())
	return err
}

func aleYesNo(p *bool) string {
	if p == nil {
		return ""
	}
	if *p {
		return "Yes"
	}
	return "No"
}

func fpsString(fps float64) string {
	if fps == float64(int64(fps)) {
		return strconv.FormatInt(int64(fps), 10)
	}
	return strconv.FormatFloat(fps, 'f', 2, 64)
}

func framesToTC(frames int64, fps float64) string {
	rate := int64(fps + 0.5)
	if rate <= 0 {
		return ""
	}
	fr := frames % rate
	sec := frames / rate
	return fmt.Sprintf("%02d:%02d:%02d:%02d", sec/3600, (sec/60)%60, sec%60, fr)
}

// ensure meta is referenced for the helper types above.
var _ = meta.Fields{}
