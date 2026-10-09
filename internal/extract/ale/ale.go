// Package ale reads Avid Log Exchange files found on a drive and matches
// their rows to clips by clip name or source file name. ALEs from ARRI
// cameras carry most of the camera metadata; the column mapping is a
// table so new column names are a data change.
package ale

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fenixstarlord/replicant/internal/clips"
	"github.com/fenixstarlord/replicant/internal/extract"
	"github.com/fenixstarlord/replicant/internal/meta"
	"github.com/fenixstarlord/replicant/internal/scan"
)

// Extractor indexes every ALE on the drive once, lazily, then answers
// per clip.
type Extractor struct {
	root    string
	files   []string // relative paths of .ale files
	once    sync.Once
	rows    map[string]Row // key: lowercase clip name or source file name
	loadErr []string
}

// Row is one ALE data row: column name -> value, plus where it came from.
type Row struct {
	File    string            `json:"file"`
	Heading map[string]string `json:"heading"`
	Values  map[string]string `json:"values"`
}

// NewFromEntries builds the extractor from a scan's entries.
func NewFromEntries(root string, entries []scan.Entry) *Extractor {
	e := &Extractor{root: root}
	for _, en := range entries {
		if !en.IsDir && !en.IsSymlink && en.Ext == "ale" && en.Error == "" {
			e.files = append(e.files, en.Path)
		}
	}
	return e
}

func (e *Extractor) Name() string  { return "ale" }
func (e *Extractor) Priority() int { return extract.PrioritySidecar }

func (e *Extractor) Available(context.Context) (bool, string) { return true, "builtin" }

func (e *Extractor) load() {
	e.rows = map[string]Row{}
	for _, rel := range e.files {
		abs := filepath.Join(e.root, filepath.FromSlash(rel))
		f, err := os.Open(abs)
		if err != nil {
			e.loadErr = append(e.loadErr, fmt.Sprintf("%s: %v", rel, err))
			continue
		}
		heading, rows, err := ParseReader(f)
		f.Close()
		if err != nil {
			e.loadErr = append(e.loadErr, fmt.Sprintf("%s: %v", rel, err))
			continue
		}
		for _, r := range rows {
			row := Row{File: rel, Heading: heading, Values: r}
			for _, key := range matchKeys(r) {
				if _, exists := e.rows[key]; !exists {
					e.rows[key] = row
				}
			}
		}
	}
}

// matchKeys lists the lowercase keys a row can be found under.
func matchKeys(r map[string]string) []string {
	var keys []string
	add := func(v string) {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" {
			return
		}
		keys = append(keys, v)
		if stem := strings.TrimSuffix(v, path.Ext(v)); stem != v {
			keys = append(keys, stem)
		}
	}
	for _, col := range []string{"Name", "Source File", "Source file", "Clip Name", "Tape"} {
		if v, ok := r[col]; ok {
			add(v)
		}
	}
	return keys
}

func (e *Extractor) lookup(c *clips.Clip) (Row, bool) {
	e.once.Do(e.load)
	if len(e.rows) == 0 {
		return Row{}, false
	}
	base := path.Base(c.PrimaryFile())
	for _, k := range []string{base, strings.TrimSuffix(base, path.Ext(base)), c.Name} {
		if r, ok := e.rows[strings.ToLower(k)]; ok {
			return r, true
		}
	}
	return Row{}, false
}

func (e *Extractor) Matches(c *clips.Clip) bool {
	if len(e.files) == 0 {
		return false
	}
	_, ok := e.lookup(c)
	return ok
}

func (e *Extractor) Extract(ctx context.Context, root string, c *clips.Clip) (*meta.Result, error) {
	row, ok := e.lookup(c)
	if !ok {
		return nil, fmt.Errorf("no ALE row for %s", c.Name)
	}
	raw, _ := json.Marshal(row)
	// An ALE's Start/End can be Avid timeline positions rather than the
	// camera's timecode; prefer timecode embedded in the media.
	return &meta.Result{Fields: MapRow(row.Values, row.Heading), Raw: raw,
		Weak: map[string]bool{"tc_start": true, "tc_end": true}}, nil
}

// ParseReader parses an ALE: a Heading section of key/value lines, a
// Column line of tab-separated names, and Data rows.
func ParseReader(r interface{ Read([]byte) (int, error) }) (heading map[string]string, rows []map[string]string, err error) {
	heading = map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	section := ""
	var columns []string
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		switch strings.TrimSpace(line) {
		case "Heading":
			section = "heading"
			continue
		case "Column":
			section = "column"
			continue
		case "Data":
			section = "data"
			continue
		case "":
			continue
		}
		switch section {
		case "heading":
			k, v, _ := strings.Cut(line, "\t")
			heading[strings.TrimSpace(k)] = strings.TrimSpace(v)
		case "column":
			columns = strings.Split(line, "\t")
			for i := range columns {
				columns[i] = strings.TrimSpace(columns[i])
			}
		case "data":
			if len(columns) == 0 {
				return nil, nil, fmt.Errorf("data before column line")
			}
			vals := strings.Split(line, "\t")
			row := make(map[string]string, len(columns))
			for i, col := range columns {
				if i < len(vals) {
					row[col] = strings.TrimSpace(vals[i])
				}
			}
			rows = append(rows, row)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, nil, err
	}
	if len(columns) == 0 {
		return nil, nil, fmt.Errorf("no Column line")
	}
	return heading, rows, nil
}

// setter writes one column value into Fields.
type setter func(f *meta.Fields, v string)

// columnMap maps lowercase ALE column names to field setters. Multiple
// names may feed one field (ARRI, Avid, RED and Sony conventions).
var columnMap = map[string]setter{
	"name":             func(f *meta.Fields, v string) { f.ClipName = meta.Str(v) },
	"start":            func(f *meta.Fields, v string) { f.TCStart = meta.Str(v) },
	"end":              func(f *meta.Fields, v string) { f.TCEnd = meta.Str(v) },
	"fps":              setFloat(func(f *meta.Fields, x float64) { f.FPS = &x }),
	"project_fps":      setFloat(func(f *meta.Fields, x float64) { f.FPS = &x }),
	"sensor_fps":       setFloat(func(f *meta.Fields, x float64) { f.CaptureFPS = &x }),
	"frame_width":      setInt(func(f *meta.Fields, n int64) { f.Width = &n }),
	"frame_height":     setInt(func(f *meta.Fields, n int64) { f.Height = &n }),
	"audio_sr":         setInt(func(f *meta.Fields, n int64) { f.SampleRate = meta.Int(kHzToHz(n)) }),
	"audio_bit":        setInt(func(f *meta.Fields, n int64) { f.AudioBitDepth = &n }),
	"exposure_index":   setInt(func(f *meta.Fields, n int64) { f.ISO = &n }),
	"iso":              setInt(func(f *meta.Fields, n int64) { f.ISO = &n }),
	"asa":              setInt(func(f *meta.Fields, n int64) { f.ISO = &n }),
	"gamma":            func(f *meta.Fields, v string) { f.ColorGamma = meta.Str(v) },
	"white_balance":    setInt(func(f *meta.Fields, n int64) { f.WBKelvin = &n }),
	"whitebalance":     setInt(func(f *meta.Fields, n int64) { f.WBKelvin = &n }),
	"cc_shift":         setFloat(func(f *meta.Fields, x float64) { f.Tint = &x }),
	"tint":             setFloat(func(f *meta.Fields, x float64) { f.Tint = &x }),
	"look_name":        func(f *meta.Fields, v string) { f.Look = meta.Str(v) },
	"shutter_angle":    setFloat(func(f *meta.Fields, x float64) { f.ShutterAngle = &x }),
	"shutter":          setFloat(func(f *meta.Fields, x float64) { f.ShutterAngle = &x }),
	"manufacturer":     func(f *meta.Fields, v string) { f.CameraMake = meta.Str(v) },
	"camera_model":     func(f *meta.Fields, v string) { f.CameraModel = meta.Str(v) },
	"camera_sn":        func(f *meta.Fields, v string) { f.CameraSerial = meta.Str(v) },
	"camera_serial":    func(f *meta.Fields, v string) { f.CameraSerial = meta.Str(v) },
	"camera_index":     func(f *meta.Fields, v string) { f.CameraIndex = meta.Str(strings.TrimRight(v, "_")) },
	"camera":           func(f *meta.Fields, v string) { f.CameraIndex = meta.Str(v) },
	"sup_version":      func(f *meta.Fields, v string) { f.Firmware = meta.Str(v) },
	"reel_name":        func(f *meta.Fields, v string) { f.Reel = meta.Str(v) },
	"reel":             func(f *meta.Fields, v string) { f.Reel = meta.Str(v) },
	"scene":            func(f *meta.Fields, v string) { f.Scene = meta.Str(v) },
	"take":             func(f *meta.Fields, v string) { f.Take = meta.Str(v) },
	"nd_filterdensity": func(f *meta.Fields, v string) { f.ND = meta.Str(zeroIsEmpty(v)) },
	"nd":               func(f *meta.Fields, v string) { f.ND = meta.Str(v) },
	"lens_type":        func(f *meta.Fields, v string) { f.Lens = meta.Str(v) },
	"lens":             func(f *meta.Fields, v string) { f.Lens = meta.Str(v) },
	"focal_length":     setFloat(func(f *meta.Fields, x float64) { f.FocalMM = &x }),
	"focal":            setFloat(func(f *meta.Fields, x float64) { f.FocalMM = &x }),
	"t_stop":           setFloat(func(f *meta.Fields, x float64) { f.TStop = &x }),
	"iris":             setFloat(func(f *meta.Fields, x float64) { f.TStop = &x }),
	"focus_distance":   func(f *meta.Fields, v string) { f.FocusDistance = meta.Str(v) },
	"circled":          func(f *meta.Fields, v string) { f.Circled = meta.Bool(isYes(v)) },
	"original_video":   setOriginalVideo,
	"codec":            func(f *meta.Fields, v string) { f.Codec = meta.Str(v) },
	"tracks":           setTracks,
}

// MapRow converts one ALE row to fields.
func MapRow(values, heading map[string]string) meta.Fields {
	var f meta.Fields
	for col, v := range values {
		if v == "" {
			continue
		}
		if set, ok := columnMap[strings.ToLower(col)]; ok {
			set(&f, v)
		}
	}
	// ARRI writes the date and time as separate columns.
	if d, t := values["Date_camera"], values["Time_camera"]; d != "" {
		if ts := parseARRIDateTime(d, t); ts != nil {
			f.RecordedAt = ts
		}
	}
	if f.FPS == nil {
		if fps, err := strconv.ParseFloat(heading["FPS"], 64); err == nil && fps > 0 {
			f.FPS = &fps
		}
	}
	if f.DurationS == nil && f.FPS != nil {
		if frames, ok := tcFrames(values["Duration"], *f.FPS); ok {
			f.FrameCount = meta.Int(frames)
			f.DurationS = meta.Float(float64(frames) / *f.FPS)
		}
	}
	return f
}

func setInt(apply func(*meta.Fields, int64)) setter {
	return func(f *meta.Fields, v string) {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			apply(f, n)
		} else if x, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			apply(f, int64(x+0.5))
		}
	}
}

func setFloat(apply func(*meta.Fields, float64)) setter {
	return func(f *meta.Fields, v string) {
		v = strings.TrimSpace(strings.TrimPrefix(v, "+"))
		if x, err := strconv.ParseFloat(v, 64); err == nil {
			apply(f, x)
		}
	}
}

// setOriginalVideo parses ARRI's "ARRICORE (3164p)" / "ARRIRAW (4.6K 3:2)" /
// "ProRes 4444 XQ" into codec and sensor mode.
func setOriginalVideo(f *meta.Fields, v string) {
	codec, mode, ok := strings.Cut(v, "(")
	f.Codec = meta.Str(strings.TrimSpace(codec))
	if ok {
		f.SensorMode = meta.Str(strings.TrimSpace(strings.TrimSuffix(mode, ")")))
	}
}

// setTracks counts audio tracks in Avid's "VA1A2A3A4A5" notation.
func setTracks(f *meta.Fields, v string) {
	n := int64(strings.Count(v, "A"))
	if n > 0 {
		f.AudioChannels = meta.Int(n)
	}
}

func zeroIsEmpty(v string) string {
	if x, err := strconv.ParseFloat(v, 64); err == nil && x == 0 {
		return ""
	}
	return v
}

func isYes(v string) bool {
	switch strings.ToLower(v) {
	case "yes", "y", "true", "1", "*":
		return true
	}
	return false
}

// parseARRIDateTime reads "20260930" + "08h46m50s".
func parseARRIDateTime(d, t string) *time.Time {
	t = strings.NewReplacer("h", ":", "m", ":", "s", "").Replace(t)
	for _, layout := range []string{"20060102 15:04:05", "20060102 15:04", "20060102 "} {
		if ts, err := time.Parse(layout, d+" "+t); err == nil {
			return meta.Time(ts)
		}
	}
	return nil
}

// tcFrames converts "HH:MM:SS:FF" at fps to a frame count.
func tcFrames(tc string, fps float64) (int64, bool) {
	parts := strings.FieldsFunc(tc, func(r rune) bool { return r == ':' || r == ';' })
	if len(parts) != 4 {
		return 0, false
	}
	var v [4]int64
	for i, p := range parts {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return 0, false
		}
		v[i] = n
	}
	rate := int64(fps + 0.5)
	return ((v[0]*60+v[1])*60+v[2])*rate + v[3], true
}

// kHzToHz expands ARRI's "48" (kHz) to 48000; values already in Hz pass through.
func kHzToHz(n int64) int64 {
	if n < 1000 {
		return n * 1000
	}
	return n
}
