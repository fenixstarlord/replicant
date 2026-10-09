package arri

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/fenixstarlord/replicant/internal/meta"
)

// Export is the shape of `art-cmd export` JSON (ARRI metadata_output
// schema v2.1): named metadata sets with free-form payloads, plus one
// block per exported frame.
type Export struct {
	Header struct {
		SoftwareVersion string `json:"softwareVersion"`
		SchemaURI       string `json:"jsonSchemaUri"`
	} `json:"arriJsonHeader"`
	ClipSets        []metadataSet `json:"clipBasedMetadataSets"`
	DescriptiveSets []metadataSet `json:"descriptiveMetadataSets"`
	FrameBased      struct {
		Frames []struct {
			FrameNo  int                        `json:"frameNo"`
			Timecode string                     `json:"timecode"`
			Sets     map[string]json.RawMessage `json:"frameBasedMetadataSets"`
		} `json:"frames"`
	} `json:"frameBasedMetadata"`
}

type metadataSet struct {
	Name    string          `json:"metadataSetName"`
	Payload json.RawMessage `json:"metadataSetPayload"`
}

// IsExport reports whether raw looks like art-cmd export output.
func IsExport(raw []byte) bool {
	return strings.Contains(string(raw[:min(len(raw), 2048)]), `"arriJsonHeader"`)
}

// payload decodes a named set into a generic map.
func (e *Export) payload(name string) map[string]any {
	for _, sets := range [][]metadataSet{e.ClipSets, e.DescriptiveSets} {
		for _, s := range sets {
			if s.Name == name && len(s.Payload) > 0 {
				var m map[string]any
				if json.Unmarshal(s.Payload, &m) == nil {
					return m
				}
			}
		}
	}
	return nil
}

func (e *Export) frameSet(name string) map[string]any {
	if len(e.FrameBased.Frames) == 0 {
		return nil
	}
	raw, ok := e.FrameBased.Frames[0].Sets[name]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		return m
	}
	return nil
}

func str(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	switch v := m[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func num(m map[string]any, key string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	switch v := m[key].(type) {
	case float64:
		return v, true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	}
	return 0, false
}

func sub(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	s, _ := m[key].(map[string]any)
	return s
}

// ratio parses "24/1" or "4/3" into a float.
func ratio(s string) (float64, bool) {
	n, d, ok := strings.Cut(s, "/")
	if !ok {
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil
	}
	a, e1 := strconv.ParseFloat(n, 64)
	b, e2 := strconv.ParseFloat(d, 64)
	if e1 != nil || e2 != nil || b == 0 {
		return 0, false
	}
	return a / b, true
}

// MapExport converts an art-cmd export into normalized fields.
func MapExport(ex *Export) meta.Fields {
	var f meta.Fields
	f.CameraMake = meta.Str("ARRI")

	cam := ex.payload("Camera Device")
	if model := str(cam, "cameraModel"); model != "" {
		f.CameraModel = meta.Str(strings.TrimSpace(strings.TrimPrefix(model, "ARRI ")))
	}
	f.CameraSerial = meta.Str(str(cam, "cameraSerialNumber"))
	f.Firmware = meta.Str(str(cam, "cameraSoftwarePackageName"))

	slate := ex.payload("Slate Info")
	f.ClipName = meta.Str(str(slate, "clipName"))
	f.Reel = meta.Str(str(slate, "reelName"))
	if idx := strings.TrimRight(str(slate, "cameraIndex"), "_"); idx != "" {
		f.CameraIndex = meta.Str(idx)
	}
	if v, ok := slate["circleTake"].(bool); ok {
		f.Circled = meta.Bool(v)
	}
	f.Scene = meta.Str(firstNonEmpty(str(slate, "sceneName"), str(slate, "scene")))
	f.Take = meta.Str(firstNonEmpty(str(slate, "takeName"), str(slate, "take")))

	clip := ex.payload("Clip Info")
	if t := str(clip, "clipCreationTime"); t != "" {
		if ts, err := time.Parse(time.RFC3339Nano, t); err == nil {
			f.RecordedAt = meta.Time(ts.UTC())
		}
	}
	if codec := str(clip, "videoCodec"); codec != "" {
		f.CodecDetail = meta.Str(codec)
		name, _, _ := strings.Cut(codec, " ")
		f.Codec = meta.Str(name) // "ARRICORE CBE Standard Profile" -> ARRICORE
	}
	f.Container = meta.Str("mxf")

	if rate := ex.payload("Project Rate"); rate != nil {
		if fps, ok := ratio(str(rate, "timebase")); ok && fps > 0 {
			f.FPS = meta.Float(fps)
		}
	}

	if sensor := ex.payload("Sensor State"); sensor != nil {
		if rect := sub(sensor, "acquisitionRect"); rect != nil {
			w, okW := num(rect, "width")
			h, okH := num(rect, "height")
			if okW && okH && w > 0 {
				f.Width, f.Height = meta.Int(int64(w)), meta.Int(int64(h))
				f.SensorMode = meta.Str(fmt.Sprintf("%dx%d", int64(w), int64(h)))
			}
		}
	}
	if dev := ex.payload("Sensor Device"); dev != nil && f.SensorMode != nil {
		if name := str(dev, "sensorName"); name != "" {
			f.SensorMode = meta.Str(*f.SensorMode + " " + name)
		}
	}
	if gen := ex.payload("MXF Generic Data"); gen != nil {
		if desc := sub(gen, "nativePictureEssenceDescriptor"); desc != nil {
			if d, ok := num(desc, "componentDepth"); ok && d > 0 {
				f.BitDepth = meta.Int(int64(d))
			}
		}
	}

	if color := ex.payload("Color"); color != nil {
		if enc := sub(color, "sceneColorEncoding"); enc != nil {
			p, c := str(enc, "sceneColorEncodingPrimaries"), str(enc, "sceneColorEncodingTransferCurve")
			if p != "" || c != "" {
				f.ColorGamma = meta.Str(strings.Trim(p+"/"+c, "/"))
			}
		}
		if look := sub(color, "lookInfo"); look != nil {
			f.Look = meta.Str(str(look, "lookFilename"))
		}
	}

	if audio := ex.payload("Audio"); audio != nil {
		if v, ok := num(audio, "audioChannels"); ok {
			f.AudioChannels = meta.Int(int64(v))
		}
		if v, ok := num(audio, "audioSampleRate"); ok {
			f.SampleRate = meta.Int(int64(v))
		}
		if v, ok := num(audio, "audioBitDepth"); ok {
			f.AudioBitDepth = meta.Int(int64(v))
		}
	}

	lens := ex.payload("Lens Device")
	f.Lens = meta.Str(str(lens, "lensModel"))
	if sq, ok := ratio(str(lens, "lensSqueezeFactor")); ok && sq > 0 && sq != 1 {
		f.Squeeze = meta.Float(sq)
	}

	// Frame 0: exposure, white balance, filter, lens state. Lens values are
	// in micrometres; iris in thousandths of a T-stop.
	if ss := ex.frameSet("Sensor State"); ss != nil {
		if ei, ok := num(ss, "exposureIndex"); ok {
			f.ISO = meta.Int(int64(ei))
		}
		if et := str(ss, "exposureTime"); et != "" {
			f.ShutterSpeed = meta.Str(et)
			if t, ok := ratio(et); ok && f.FPS != nil && t > 0 {
				f.ShutterAngle = meta.Float(roundTo(360*t**f.FPS, 0.1))
			}
		}
		if sr, ok := ratio(str(ss, "sensorSampleRate")); ok && sr > 0 {
			f.CaptureFPS = meta.Float(sr)
		}
	}
	if wb := ex.frameSet("White Balance"); wb != nil {
		if ct, ok := num(wb, "colorTemperature"); ok {
			f.WBKelvin = meta.Int(int64(ct))
		}
		if tint, ok := num(wb, "whiteBalanceTint"); ok {
			f.Tint = meta.Float(roundTo(tint, 0.1))
		}
	}
	if filt := ex.frameSet("Filter"); filt != nil {
		if nd, ok := num(filt, "ndFilterDensity"); ok {
			if nd > 0 {
				f.ND = meta.Str(strconv.FormatFloat(roundTo(nd, 0.1), 'f', -1, 64))
			} else {
				f.ND = meta.Str("clear")
			}
		}
	}
	if ls := ex.frameSet("Lens State"); ls != nil {
		if fl, ok := num(ls, "lensFocalLength"); ok && fl > 0 {
			f.FocalMM = meta.Float(roundTo(fl/1000, 0.1))
		}
		if iris, ok := num(ls, "lensIris"); ok && iris > 0 {
			f.TStop = meta.Float(roundTo(iris/1000, 0.1))
		}
		if fd, ok := num(ls, "lensFocusDistanceMetric"); ok && fd > 0 {
			f.FocusDistance = meta.Str(fmt.Sprintf("%.2f m", fd/1000))
		}
	}
	if len(ex.FrameBased.Frames) > 0 {
		f.TCStart = meta.Str(ex.FrameBased.Frames[0].Timecode)
	}
	return f
}

// roundTo rounds to a decimal step (0.1, 0.01) without binary noise.
func roundTo(x, step float64) float64 {
	n := math.Round(1 / step)
	return math.Round(x*n) / n
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
