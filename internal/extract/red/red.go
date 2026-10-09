// Package red extracts R3D metadata with REDline, the command-line tool
// shipped with REDCINE-X PRO. Verified against REDline from REDCINE-X
// PRO on a V-RAPTOR [X] clip (testdata/golden/red).
package red

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fenixstarlord/indexserver/internal/clips"
	"github.com/fenixstarlord/indexserver/internal/extract"
	"github.com/fenixstarlord/indexserver/internal/meta"
)

// DefaultPaths are where REDCINE-X PRO installs REDline on macOS.
var DefaultPaths = []string{
	"/Applications/REDCINE-X Professional/REDCINE-X PRO.app/Contents/MacOS/REDline",
	"/Applications/REDCINE-X PRO.app/Contents/MacOS/REDline",
	"/Applications/REDCINE-X PRO/REDline",
	"/Applications/REDline/REDline",
	"/usr/local/bin/REDline",
}

// Extractor runs REDline. Path may be empty to use the defaults.
type Extractor struct {
	Path string
	bin  string
}

func (e *Extractor) Name() string  { return "redline" }
func (e *Extractor) Priority() int { return extract.PriorityVendor }

// BinPath is the resolved tool path after Available ran.
func (e *Extractor) BinPath() string { return e.bin }

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

// Matches accepts .RDC clips and loose R3D segments.
func (e *Extractor) Matches(c *clips.Clip) bool {
	if c.Kind == clips.KindR3D {
		return true
	}
	return strings.EqualFold(path.Ext(c.PrimaryFile()), ".r3d")
}

func (e *Extractor) Extract(ctx context.Context, root string, c *clips.Clip) (*meta.Result, error) {
	abs := filepath.Join(root, filepath.FromSlash(c.PrimaryFile()))
	// --useMeta reads the clip's own settings; without it REDline prints
	// its defaults for ISO, Kelvin and friends.
	out, err := extract.RunTool(ctx, e.bin, "--i", abs, "--useMeta", "--printMeta", "1")
	kv := ParseKV(string(out))
	// REDline exits non-zero after printing (it has no output job to run);
	// the metadata is still complete when the clip block is present.
	if err != nil && kv["Clip Name"] == "" {
		return nil, err
	}
	raw, _ := json.Marshal(map[string]any{"printMeta": kv})
	return &meta.Result{Fields: Map(kv), Raw: raw}, nil
}

// ParseKV reads REDline's "Key:<tab>Value" lines into a map, skipping the
// log banner. Keys may themselves contain ": " (e.g. "LGG Lift: Red").
func ParseKV(s string) map[string]string {
	kv := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		k, v, ok := strings.Cut(line, ":\t")
		if !ok {
			if k, v, ok = strings.Cut(line, ":"); !ok {
				continue
			}
		}
		k = strings.TrimSuffix(strings.TrimSpace(k), ":")
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		if _, dup := kv[k]; !dup { // first occurrence wins (Aperture, Focal Length repeat)
			kv[k] = v
		}
	}
	return kv
}

func num(kv map[string]string, key string) (float64, bool) {
	v := strings.TrimSpace(kv[key])
	v = strings.TrimPrefix(strings.TrimPrefix(v, "F"), "T")
	v = strings.TrimSuffix(strings.TrimSuffix(v, "mm"), "°")
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	return f, err == nil
}

func round1(x float64) float64 { return float64(int64(x*10+0.5)) / 10 }

// Map converts REDline keys to fields.
func Map(kv map[string]string) meta.Fields {
	var f meta.Fields
	f.Codec = meta.Str("R3D")
	f.Container = meta.Str("r3d")
	f.CameraMake = meta.Str("RED")
	if rc := kv["REDCODE"]; rc != "" {
		f.CodecDetail = meta.Str("REDCODE " + rc)
	}
	f.ClipName = meta.Str(kv["ReelID"])
	f.Reel = meta.Str(kv["CamReelID"])
	f.CameraIndex = meta.Str(kv["Camera"])
	f.CameraModel = meta.Str(kv["Camera Model"])
	f.CameraSerial = meta.Str(kv["Camera PIN"])
	f.Firmware = meta.Str(kv["Firmware Version"])
	f.TCStart = meta.Str(kv["Abs TC"])
	f.TCEnd = meta.Str(kv["End Abs TC"])
	f.Scene = meta.Str(kv["Scene"])
	f.Take = meta.Str(kv["Take"])
	if c := strings.ToLower(kv["Circle"]); c != "" {
		f.Circled = meta.Bool(c == "yes" || c == "true" || c == "1")
	}
	f.Look = meta.Str(kv["Look Name"])
	if w, ok := num(kv, "Frame Width"); ok {
		f.Width = meta.Int(int64(w))
	}
	if h, ok := num(kv, "Frame Height"); ok {
		f.Height = meta.Int(int64(h))
	}
	if f.Width != nil && f.Height != nil {
		mode := fmt.Sprintf("%dx%d", *f.Width, *f.Height)
		if sn := kv["Sensor Name"]; sn != "" {
			mode += " " + sn
		}
		f.SensorMode = meta.Str(mode)
	}
	if v, ok := num(kv, "FPS"); ok {
		f.FPS = meta.Float(v)
	}
	if v, ok := num(kv, "Record FPS"); ok {
		f.CaptureFPS = meta.Float(v)
	}
	if v, ok := num(kv, "Total Frames"); ok {
		f.FrameCount = meta.Int(int64(v))
		if f.FPS != nil && *f.FPS > 0 {
			f.DurationS = meta.Float(v / *f.FPS)
		}
	}
	if v, ok := num(kv, "ISO"); ok {
		f.ISO = meta.Int(int64(v))
	}
	if v, ok := num(kv, "Kelvin"); ok {
		f.WBKelvin = meta.Int(int64(v))
	}
	if v, ok := num(kv, "Tint"); ok {
		f.Tint = meta.Float(v)
	}
	if v, ok := num(kv, "Shutter (deg)"); ok {
		f.ShutterAngle = meta.Float(round1(v))
	}
	if v, ok := num(kv, "Shutter (1/sec)"); ok {
		f.ShutterSpeed = meta.Str("1/" + strconv.FormatFloat(v, 'f', -1, 64))
	}
	if v, ok := num(kv, "ND Stops"); ok && v > 0 {
		f.ND = meta.Str(fmt.Sprintf("%.2f stops", v))
	}
	lens := kv["Lens Name"]
	if lens == "" {
		lens = kv["Lens"]
	}
	if b := kv["Lens Brand"]; b != "" && lens != "" && !strings.Contains(lens, b) {
		lens = b + " " + lens
	}
	f.Lens = meta.Str(lens)
	if v, ok := num(kv, "Focal Length"); ok {
		f.FocalMM = meta.Float(v)
	}
	if v, ok := num(kv, "Aperture"); ok {
		f.TStop = meta.Float(v)
	}
	if v, ok := num(kv, "Focus Distance"); ok && v > 0 {
		f.FocusDistance = meta.Str(fmt.Sprintf("%.2f m", v/1000))
	}
	if v, ok := num(kv, "Camera Audio Channels"); ok && v > 0 {
		f.AudioChannels = meta.Int(int64(v))
	}
	if v, ok := num(kv, "Pixel Aspect Ratio"); ok && v > 0 && v != 1 {
		f.Squeeze = meta.Float(v)
	}
	switch kv["Clip Current Image Pipeline"] {
	case "IPP2":
		f.ColorGamma = meta.Str("REDWideGamutRGB/Log3G10")
	default:
		if cs, gs := kv["Color Space"], kv["Gamma Space"]; cs != "" || gs != "" {
			f.ColorGamma = meta.Str("colorspace " + cs + "/gamma " + gs)
		}
	}
	if d, t := kv["Date"], kv["Timestamp"]; len(d) == 8 {
		if ts, err := time.Parse("20060102 150405", d+" "+fmt.Sprintf("%06s", t)); err == nil {
			f.RecordedAt = meta.Time(ts)
		} else if ts, err := time.Parse("20060102", d); err == nil {
			f.RecordedAt = meta.Time(ts)
		}
	}
	return f
}
