// Package red extracts R3D metadata with REDline, the command-line tool
// shipped free with REDCINE-X PRO.
//
// The exact output of `REDline --printMeta` has not yet been captured
// from a real install (see docs/design and AGENTS.md); the parser below
// handles "Key: Value" lines and will be pinned to golden output when
// the tool is installed.
package red

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fenixstarlord/indexserver/internal/clips"
	"github.com/fenixstarlord/indexserver/internal/extract"
	"github.com/fenixstarlord/indexserver/internal/meta"
)

// DefaultPaths are where REDline is found. The macOS REDCINE-X PRO
// installer puts the app under /Applications/REDCINE-X Professional/ with
// REDline inside the bundle; on Linux the standalone REDline archive is
// unpacked by hand (see docs/tools.md). PATH is searched last.
var DefaultPaths = []string{
	"/Applications/REDCINE-X Professional/REDCINE-X PRO.app/Contents/MacOS/REDline",
	"/Applications/REDCINE-X PRO.app/Contents/MacOS/REDline",
	"/Applications/REDCINE-X PRO/REDline",
	"/Applications/REDline/REDline",
	"/usr/local/bin/REDline",
	"/opt/REDline/REDline",
}

// Extractor runs REDline. Path may be empty to use the defaults.
type Extractor struct {
	Path string
	bin  string
}

func (e *Extractor) Name() string  { return "redline" }
func (e *Extractor) Priority() int { return extract.PriorityVendor }

func (e *Extractor) Available(ctx context.Context) (bool, string) {
	e.bin = extract.FindTool(e.Path, "REDline", DefaultPaths...)
	if e.bin == "" {
		return false, ""
	}
	// REDline has no --version; its usage banner carries the version.
	v := extract.VersionLine(ctx, e.bin, "--version")
	if strings.Contains(v, "Unknown command") || v == "" {
		v = "found"
	}
	return true, v
}

func (e *Extractor) Matches(c *clips.Clip) bool { return c.Kind == clips.KindR3D }

func (e *Extractor) Extract(ctx context.Context, root string, c *clips.Clip) (*meta.Result, error) {
	abs := filepath.Join(root, filepath.FromSlash(c.PrimaryFile()))
	out, err := extract.RunTool(ctx, e.bin, "--i", abs, "--printMeta", "1")
	if err != nil {
		return nil, err
	}
	kv := ParseKV(string(out))
	raw, _ := json.Marshal(map[string]any{"printMeta": kv, "output": string(out)})
	return &meta.Result{Fields: Map(kv), Raw: raw}, nil
}

// ParseKV reads "Key: Value" lines into a map.
func ParseKV(s string) map[string]string {
	kv := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k != "" && v != "" {
			kv[k] = v
		}
	}
	return kv
}

// Map converts REDline keys to fields. Keys are matched case-insensitively
// against several known spellings.
func Map(kv map[string]string) meta.Fields {
	var f meta.Fields
	get := func(keys ...string) string {
		for _, k := range keys {
			for kk, v := range kv {
				if strings.EqualFold(kk, k) {
					return v
				}
			}
		}
		return ""
	}
	num := func(s string) (float64, bool) {
		s = strings.TrimSpace(strings.TrimRight(s, "mmKkKfpsFPS°"))
		x, err := strconv.ParseFloat(strings.Fields(s + " ")[0], 64)
		return x, err == nil
	}
	f.Codec = meta.Str("R3D")
	f.CameraMake = meta.Str("RED")
	f.ClipName = meta.Str(get("Clip Name", "Reel ID", "Clip"))
	f.Reel = meta.Str(get("Reel ID", "Reel"))
	f.CameraModel = meta.Str(get("Camera Type", "Camera Model", "Camera"))
	f.CameraSerial = meta.Str(get("Camera PIN", "Camera Serial", "Serial"))
	f.Firmware = meta.Str(get("Firmware Version", "Firmware"))
	f.TCStart = meta.Str(get("Start Absolute Timecode", "Start Edge Timecode", "Timecode", "Start TC"))
	f.TCEnd = meta.Str(get("End Absolute Timecode", "End Edge Timecode", "End TC"))
	f.Lens = meta.Str(get("Lens Name", "Lens", "Lens Model"))
	f.SensorMode = meta.Str(get("Record Format", "Format", "Sensor Mode"))
	f.ColorGamma = meta.Str(get("Color Space", "Gamma Curve"))
	f.Look = meta.Str(get("Look", "LUT"))
	f.ND = meta.Str(get("ND Filter", "ND"))
	f.FocusDistance = meta.Str(get("Focus Distance", "Focus"))
	if v, ok := num(get("Frame Width", "Width")); ok {
		f.Width = meta.Int(int64(v))
	}
	if v, ok := num(get("Frame Height", "Height")); ok {
		f.Height = meta.Int(int64(v))
	}
	if v, ok := num(get("FPS", "Frame Rate", "Record FPS")); ok {
		f.FPS = meta.Float(v)
	}
	if v, ok := num(get("Sensor FPS", "Capture FPS")); ok {
		f.CaptureFPS = meta.Float(v)
	}
	if v, ok := num(get("Total Frames", "Frame Count", "Frames")); ok {
		f.FrameCount = meta.Int(int64(v))
	}
	if v, ok := num(get("ISO")); ok {
		f.ISO = meta.Int(int64(v))
	}
	if v, ok := num(get("Color Temp", "Color Temperature", "Kelvin")); ok {
		f.WBKelvin = meta.Int(int64(v))
	}
	if v, ok := num(get("Tint")); ok {
		f.Tint = meta.Float(v)
	}
	if v, ok := num(get("Shutter Angle", "Shutter (deg)")); ok {
		f.ShutterAngle = meta.Float(v)
	}
	if v := get("Shutter", "Exposure Time"); v != "" {
		f.ShutterSpeed = meta.Str(v)
	}
	if v, ok := num(get("Focal Length")); ok {
		f.FocalMM = meta.Float(v)
	}
	if v, ok := num(strings.TrimPrefix(strings.TrimPrefix(get("Aperture", "T-Stop", "F-Stop"), "T"), "f")); ok {
		f.TStop = meta.Float(v)
	}
	if v := get("Compression", "REDCODE"); v != "" {
		f.CodecDetail = meta.Str(v)
	}
	if v, ok := num(get("Anamorphic", "Pixel Aspect Ratio")); ok && v > 0 && v != 1 {
		f.Squeeze = meta.Float(v)
	}
	if f.FPS != nil && f.FrameCount != nil {
		f.DurationS = meta.Float(float64(*f.FrameCount) / *f.FPS)
	}
	return f
}
