// Package meta is the normalized metadata model shared by extractors, the
// bundle, and the server. Every field is optional; a nil pointer means
// "unknown". Field names match the clips table columns.
package meta

import (
	"encoding/json"
	"reflect"
	"sort"
	"time"
)

// Fields is the normalized, filterable metadata of one clip.
type Fields struct {
	// Clip
	ClipName    *string    `json:"clip_name,omitempty"`
	Reel        *string    `json:"reel,omitempty"`
	CameraIndex *string    `json:"camera_index,omitempty"`
	Scene       *string    `json:"scene,omitempty"`
	Take        *string    `json:"take,omitempty"`
	Circled     *bool      `json:"circled,omitempty"`
	RecordedAt  *time.Time `json:"recorded_at,omitempty"`
	TCStart     *string    `json:"tc_start,omitempty"`
	TCEnd       *string    `json:"tc_end,omitempty"`
	DurationS   *float64   `json:"duration_s,omitempty"`
	FrameCount  *int64     `json:"frame_count,omitempty"`
	// Format
	Container   *string  `json:"container,omitempty"`
	Codec       *string  `json:"codec,omitempty"`
	CodecDetail *string  `json:"codec_detail,omitempty"`
	Width       *int64   `json:"width,omitempty"`
	Height      *int64   `json:"height,omitempty"`
	SensorMode  *string  `json:"sensor_mode,omitempty"`
	FPS         *float64 `json:"fps,omitempty"`
	CaptureFPS  *float64 `json:"capture_fps,omitempty"`
	BitDepth    *int64   `json:"bit_depth,omitempty"`
	ColorGamma  *string  `json:"color_gamma,omitempty"`
	Squeeze     *float64 `json:"squeeze,omitempty"`
	// Camera
	CameraMake   *string `json:"camera_make,omitempty"`
	CameraModel  *string `json:"camera_model,omitempty"`
	CameraSerial *string `json:"camera_serial,omitempty"`
	Firmware     *string `json:"firmware,omitempty"`
	// Exposure
	ISO          *int64   `json:"iso,omitempty"`
	WBKelvin     *int64   `json:"wb_kelvin,omitempty"`
	Tint         *float64 `json:"tint,omitempty"`
	ShutterAngle *float64 `json:"shutter_angle,omitempty"`
	ShutterSpeed *string  `json:"shutter_speed,omitempty"`
	ND           *string  `json:"nd,omitempty"`
	// Lens
	Lens          *string  `json:"lens,omitempty"`
	FocalMM       *float64 `json:"focal_mm,omitempty"`
	TStop         *float64 `json:"t_stop,omitempty"`
	FocusDistance *string  `json:"focus_distance,omitempty"`
	// Audio
	AudioChannels *int64 `json:"audio_channels,omitempty"`
	SampleRate    *int64 `json:"sample_rate,omitempty"`
	AudioBitDepth *int64 `json:"audio_bit_depth,omitempty"`
	// Look
	Look *string `json:"look,omitempty"`
}

// Result is what one extractor produces for one clip.
type Result struct {
	Source string          // extractor name
	Fields Fields          // normalized fields this source could fill
	Raw    json.RawMessage // verbatim output, stored as-is
	// Weak lists json field names this source is not authoritative for.
	// A weak value is used only when no other source supplies the field,
	// regardless of priority (e.g. an ALE's Start column versus the
	// timecode embedded in the media).
	Weak map[string]bool
}

// Merged is the combination of several results.
type Merged struct {
	Fields  Fields                     `json:"fields"`
	Sources map[string]string          `json:"sources"` // field json name -> source
	Raw     map[string]json.RawMessage `json:"raw"`     // source -> verbatim output
	Errors  []string                   `json:"errors,omitempty"`
}

// Merge combines results in the given order: the first non-nil value for
// each field wins, so callers pass results in priority order
// (vendor tool, then sidecar, then ffprobe).
func Merge(results []Result) Merged {
	m := Merged{Sources: map[string]string{}, Raw: map[string]json.RawMessage{}}
	dst := reflect.ValueOf(&m.Fields).Elem()
	typ := dst.Type()
	for _, r := range results {
		if len(r.Raw) > 0 {
			m.Raw[r.Source] = r.Raw
		}
	}
	// Two passes: strong values in priority order, then weak ones.
	for _, weakPass := range []bool{false, true} {
		for _, r := range results {
			src := reflect.ValueOf(r.Fields)
			for i := 0; i < typ.NumField(); i++ {
				name := jsonName(typ.Field(i))
				if r.Weak[name] != weakPass {
					continue
				}
				if !dst.Field(i).IsNil() || src.Field(i).IsNil() {
					continue
				}
				dst.Field(i).Set(src.Field(i))
				m.Sources[name] = r.Source
			}
		}
	}
	return m
}

// Set returns the json names of fields that are non-nil, sorted.
func (f Fields) Set() []string {
	v := reflect.ValueOf(f)
	typ := v.Type()
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		if !v.Field(i).IsNil() {
			out = append(out, jsonName(typ.Field(i)))
		}
	}
	sort.Strings(out)
	return out
}

func jsonName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	for i, c := range tag {
		if c == ',' {
			return tag[:i]
		}
	}
	return tag
}

// Helpers for building Fields without temporaries.

func Str(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func Int(n int64) *int64       { return &n }
func Float(x float64) *float64 { return &x }
func Bool(b bool) *bool        { return &b }
func Time(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
