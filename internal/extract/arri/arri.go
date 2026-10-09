// Package arri extracts ARRIRAW, ARRICORE and ARRI ProRes metadata with
// the ARRI Reference Tool command line (art-cmd).
//
// The exact export flags and output of art-cmd have not yet been
// captured from a real install (see AGENTS.md "Unknowns"). The runner
// below takes its arguments from config so they can be fixed without a
// rebuild, and the mapper matches ARRI's documented metadata names.
package arri

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/fenixstarlord/indexserver/internal/clips"
	"github.com/fenixstarlord/indexserver/internal/extract"
	"github.com/fenixstarlord/indexserver/internal/meta"
)

// DefaultPaths are likely art-cmd locations. ART CMD is a separate
// download from the ARRI Reference Tool GUI app and ships as an archive
// with no installer, so these are the conventional places to unpack or
// link it (see docs/tools.md). PATH is searched last.
var DefaultPaths = []string{
	"/Applications/ARRI Reference Tool CMD/bin/art-cmd",
	"~/Applications/ARRI Reference Tool CMD/bin/art-cmd",
	"/Applications/ARRI Reference Tool CMD/art-cmd",
	"/Applications/art-cmd/bin/art-cmd",
	"/usr/local/bin/art-cmd",
	"/opt/homebrew/bin/art-cmd",
	"/opt/arri/art-cmd/bin/art-cmd",
}

// DefaultArgs is the metadata-only export from the ART CMD 1.0.0 user
// manual (section 3.4.1): `art-cmd export --input <clip> --output <file>.json`
// writes static clip metadata and dynamic frame metadata as one JSON
// document and skips audio and look files. {input} and {outdir} are
// substituted; the file name keeps the extractor's JSON lookup simple.
// Override via config `tools.art_cmd_args`.
// --duration 1 keeps the per-frame block to a single frame.
var DefaultArgs = []string{"export", "--input", "{input}", "--duration", "1", "--output", "{outdir}/metadata.json"}

// clipNameRe matches ARRI clip names: A001C001_..., A_0001C001_..., B021C004_...
var clipNameRe = regexp.MustCompile(`(?i)^[A-Z]_?\d{3,4}C\d{3}_`)

// Extractor runs art-cmd.
type Extractor struct {
	Path string
	Args []string
	bin  string
}

func (e *Extractor) Name() string  { return "art-cmd" }
func (e *Extractor) Priority() int { return extract.PriorityVendor }

// BinPath is the resolved tool path after Available ran.
func (e *Extractor) BinPath() string { return e.bin }

func (e *Extractor) Available(ctx context.Context) (bool, string) {
	paths := make([]string, 0, len(DefaultPaths))
	for _, p := range DefaultPaths {
		if strings.HasPrefix(p, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				p = filepath.Join(home, p[2:])
			}
		}
		paths = append(paths, p)
	}
	e.bin = extract.FindTool(e.Path, "art-cmd", paths...)
	if e.bin == "" {
		return false, ""
	}
	v := extract.VersionLine(ctx, e.bin, "--version")
	if strings.Contains(v, "Library not loaded") || strings.Contains(v, "disallowed by system policy") {
		// Downloaded but still quarantined: macOS refuses to load its libraries.
		return false, "blocked by macOS quarantine; see docs/tools.md"
	}
	return true, v
}

// Matches accepts ARRIRAW sequences and ARRI-named MXF/MOV/ARI/ARX clips.
func (e *Extractor) Matches(c *clips.Clip) bool {
	if c.Kind == clips.KindARRIRAW {
		return true
	}
	p := c.PrimaryFile()
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(p), "."))
	switch ext {
	case "ari", "arx":
		return true
	case "mxf", "mov":
		return clipNameRe.MatchString(path.Base(p))
	}
	return false
}

func (e *Extractor) Extract(ctx context.Context, root string, c *clips.Clip) (*meta.Result, error) {
	// A frame sequence is given as its folder; art-cmd reads the frames.
	in := c.PrimaryFile()
	if c.Kind == clips.KindARRIRAW {
		in = c.RootPath
	}
	abs := filepath.Join(root, filepath.FromSlash(in))
	outdir, err := os.MkdirTemp("", "shelf-art-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(outdir)
	args := e.Args
	if len(args) == 0 {
		args = DefaultArgs
	}
	full := make([]string, len(args))
	for i, a := range args {
		full[i] = strings.NewReplacer("{input}", abs, "{outdir}", outdir).Replace(a)
	}
	out, err := extract.RunTool(ctx, e.bin, full...)
	if err != nil {
		return nil, err
	}
	raw := out
	// Prefer a JSON file the tool wrote to the temp dir, if any.
	if entries, _ := os.ReadDir(outdir); len(entries) > 0 {
		for _, en := range entries {
			if strings.HasSuffix(strings.ToLower(en.Name()), ".json") {
				if b, err := os.ReadFile(filepath.Join(outdir, en.Name())); err == nil {
					raw = b
					break
				}
			}
		}
	}
	var kv map[string]any
	if err := json.Unmarshal(raw, &kv); err != nil {
		return nil, fmt.Errorf("art-cmd output is not JSON (flags may need adjusting): %w", err)
	}
	return &meta.Result{Fields: Map(flatten(kv)), Raw: json.RawMessage(raw)}, nil
}

// flatten turns nested JSON into "a.b.c" keys with string values.
func flatten(m map[string]any) map[string]string {
	out := map[string]string{}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, vv := range x {
				key := k
				if prefix != "" {
					key = prefix + "." + k
				}
				walk(key, vv)
			}
		case []any:
			for i, vv := range x {
				walk(prefix+"."+strconv.Itoa(i), vv)
			}
		case string:
			out[prefix] = x
		case float64:
			out[prefix] = strconv.FormatFloat(x, 'f', -1, 64)
		case bool:
			out[prefix] = strconv.FormatBool(x)
		}
	}
	walk("", m)
	return out
}

// Map matches flattened keys by their last path segment against ARRI's
// metadata names (the same ones that appear in ARRI ALE columns).
func Map(kv map[string]string) meta.Fields {
	byLeaf := map[string]string{}
	for k, v := range kv {
		leaf := strings.ToLower(k[strings.LastIndex(k, ".")+1:])
		leaf = strings.NewReplacer(" ", "_", "-", "_").Replace(leaf)
		if _, exists := byLeaf[leaf]; !exists {
			byLeaf[leaf] = v
		}
	}
	get := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := byLeaf[strings.ToLower(k)]; ok && v != "" {
				return v
			}
		}
		return ""
	}
	num := func(s string) (float64, bool) {
		x, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(s, "+")), 64)
		return x, err == nil
	}
	var f meta.Fields
	f.CameraMake = meta.Str("ARRI")
	f.ClipName = meta.Str(get("clip_name", "name", "clipname"))
	f.Reel = meta.Str(get("reel_name", "reel"))
	f.CameraModel = meta.Str(get("camera_model", "cameramodel", "model"))
	f.CameraSerial = meta.Str(get("camera_serial_number", "camera_sn", "serial_number"))
	f.CameraIndex = meta.Str(strings.TrimRight(get("camera_index", "cameraindex"), "_"))
	f.Firmware = meta.Str(get("sup_version", "firmware", "software_version"))
	f.TCStart = meta.Str(get("timecode_start", "start_timecode", "master_tc", "tc_start", "start"))
	f.TCEnd = meta.Str(get("timecode_end", "end_timecode", "tc_end", "end"))
	f.ColorGamma = meta.Str(get("gamma", "color_gamma", "target_color_space"))
	f.Look = meta.Str(get("look_name", "look_file", "look"))
	f.Lens = meta.Str(get("lens_type", "lens_model", "lens"))
	f.ND = meta.Str(get("nd_filter_density", "nd_filterdensity", "nd_filter"))
	f.FocusDistance = meta.Str(get("focus_distance", "lens_focus_distance"))
	f.SensorMode = meta.Str(get("sensor_mode", "recording_area", "image_format"))
	if v := get("original_video", "codec", "video_codec", "recording_format"); v != "" {
		codec, mode, ok := strings.Cut(v, "(")
		f.Codec = meta.Str(strings.TrimSpace(codec))
		if ok && f.SensorMode == nil {
			f.SensorMode = meta.Str(strings.TrimSpace(strings.TrimSuffix(mode, ")")))
		}
	}
	if v, ok := num(get("exposure_index", "ei", "iso")); ok {
		f.ISO = meta.Int(int64(v))
	}
	if v, ok := num(get("white_balance", "wb", "color_temperature")); ok {
		f.WBKelvin = meta.Int(int64(v))
	}
	if v, ok := num(get("cc_shift", "tint", "white_balance_tint")); ok {
		f.Tint = meta.Float(v)
	}
	if v, ok := num(get("shutter_angle", "shutter")); ok {
		f.ShutterAngle = meta.Float(v)
	}
	if v, ok := num(get("sensor_fps", "capture_fps")); ok {
		f.CaptureFPS = meta.Float(v)
	}
	if v, ok := num(get("project_fps", "fps", "frame_rate")); ok {
		f.FPS = meta.Float(v)
	}
	if v, ok := num(get("frame_width", "image_width", "width")); ok {
		f.Width = meta.Int(int64(v))
	}
	if v, ok := num(get("frame_height", "image_height", "height")); ok {
		f.Height = meta.Int(int64(v))
	}
	if v, ok := num(get("bit_depth", "bits_per_sample")); ok {
		f.BitDepth = meta.Int(int64(v))
	}
	if v, ok := num(get("focal_length", "lens_focal_length")); ok {
		f.FocalMM = meta.Float(v)
	}
	if v, ok := num(strings.TrimPrefix(get("iris", "t_stop", "lens_iris", "aperture"), "T")); ok {
		f.TStop = meta.Float(v)
	}
	if v, ok := num(get("lens_squeeze", "squeeze_factor", "anamorphic_squeeze")); ok && v > 0 && v != 1 {
		f.Squeeze = meta.Float(v)
	}
	if v, ok := num(get("frame_count", "frames", "duration_frames")); ok {
		f.FrameCount = meta.Int(int64(v))
	}
	if f.FPS != nil && f.FrameCount != nil {
		f.DurationS = meta.Float(float64(*f.FrameCount) / *f.FPS)
	}
	return f
}
