// Package braw reads the JSON .sidecar file Blackmagic RAW clips carry.
// Blackmagic ships no CLI, so the sidecar plus ffprobe is the v1 source;
// a helper on the Blackmagic RAW SDK may come later.
package braw

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fenixstarlord/indexserver/internal/clips"
	"github.com/fenixstarlord/indexserver/internal/extract"
	"github.com/fenixstarlord/indexserver/internal/meta"
)

// Extractor parses .sidecar JSON.
type Extractor struct{}

func (Extractor) Name() string                             { return "braw-sidecar" }
func (Extractor) Priority() int                            { return extract.PrioritySidecar }
func (Extractor) Available(context.Context) (bool, string) { return true, "builtin" }

func (Extractor) Matches(c *clips.Clip) bool {
	return c.Kind == clips.KindBRAW && len(c.SidecarsWithExt("sidecar")) > 0
}

func (Extractor) Extract(ctx context.Context, root string, c *clips.Clip) (*meta.Result, error) {
	p := c.SidecarsWithExt("sidecar")[0]
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil {
		return nil, err
	}
	var kv map[string]any
	if err := json.Unmarshal(raw, &kv); err != nil {
		return nil, fmt.Errorf("sidecar json: %w", err)
	}
	return &meta.Result{Fields: Map(kv), Raw: json.RawMessage(raw)}, nil
}

// Map converts sidecar keys to fields. Keys follow the Blackmagic RAW SDK
// metadata names; several spellings are accepted because the sidecar
// format is only partially documented. Unknown keys stay in Raw.
func Map(kv map[string]any) meta.Fields {
	var f meta.Fields
	get := func(keys ...string) (any, bool) {
		for _, k := range keys {
			for kk, v := range kv {
				if strings.EqualFold(kk, k) {
					return v, true
				}
			}
		}
		return nil, false
	}
	if v, ok := get("iso"); ok {
		if n, ok := toInt(v); ok {
			f.ISO = meta.Int(n)
		}
	}
	if v, ok := get("white_balance_kelvin", "white_balance", "whiteBalanceKelvin"); ok {
		if n, ok := toInt(v); ok {
			f.WBKelvin = meta.Int(n)
		}
	}
	if v, ok := get("white_balance_tint", "tint", "whiteBalanceTint"); ok {
		if x, ok := toFloat(v); ok {
			f.Tint = meta.Float(x)
		}
	}
	if v, ok := get("shutter_angle", "shutterAngle"); ok {
		if x, ok := toFloat(v); ok {
			f.ShutterAngle = meta.Float(x)
		}
	}
	if v, ok := get("shutter_value", "shutter_speed", "shutterValue"); ok {
		f.ShutterSpeed = meta.Str(toString(v))
	}
	if v, ok := get("nd_filter", "ndFilter"); ok {
		f.ND = meta.Str(toString(v))
	}
	if v, ok := get("lens_type", "lensType", "lens"); ok {
		f.Lens = meta.Str(toString(v))
	}
	if v, ok := get("focal_length", "focalLength"); ok {
		if x, ok := toFloat(v); ok {
			f.FocalMM = meta.Float(x)
		}
	}
	if v, ok := get("aperture", "fstop", "iris"); ok {
		if x, ok := toFloat(strings.TrimPrefix(strings.TrimPrefix(toString(v), "f"), "T")); ok {
			f.TStop = meta.Float(x)
		}
	}
	if v, ok := get("distance", "focus_distance", "focusDistance"); ok {
		f.FocusDistance = meta.Str(toString(v))
	}
	if v, ok := get("lut_name", "lutName", "look"); ok {
		f.Look = meta.Str(toString(v))
	}
	if v, ok := get("gamma", "color_science_gamma"); ok {
		g := toString(v)
		if gm, ok := get("gamut", "color_space"); ok {
			g = toString(gm) + " / " + g
		}
		f.ColorGamma = meta.Str(g)
	}
	if v, ok := get("camera_type", "cameraType", "camera_model"); ok {
		f.CameraModel = meta.Str(toString(v))
		f.CameraMake = meta.Str("Blackmagic Design")
	}
	if v, ok := get("camera_id", "cameraId", "camera_serial"); ok {
		f.CameraSerial = meta.Str(toString(v))
	}
	if v, ok := get("firmware_version", "firmwareVersion"); ok {
		f.Firmware = meta.Str(toString(v))
	}
	if v, ok := get("reel_name", "reelName", "reel"); ok {
		f.Reel = meta.Str(toString(v))
	}
	if v, ok := get("scene"); ok {
		f.Scene = meta.Str(toString(v))
	}
	if v, ok := get("take"); ok {
		f.Take = meta.Str(toString(v))
	}
	if v, ok := get("good_take", "goodTake", "circled"); ok {
		s := strings.ToLower(toString(v))
		f.Circled = meta.Bool(s == "true" || s == "1" || s == "yes")
	}
	if v, ok := get("anamorphic_ratio", "anamorphicRatio", "squeeze"); ok {
		if x, ok := toFloat(v); ok && x > 0 {
			f.Squeeze = meta.Float(x)
		}
	}
	if v, ok := get("sensor_area_captured", "sensorMode", "sensor_mode"); ok {
		f.SensorMode = meta.Str(toString(v))
	}
	return f
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

func toInt(v any) (int64, bool) {
	f, ok := toFloat(v)
	return int64(f + 0.5), ok
}
