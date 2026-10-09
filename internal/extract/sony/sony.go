// Package sony reads the NonRealTimeMeta XML sidecar that Sony cameras
// write next to each clip (XDROOT/Clip/<name>M01.XML, M4ROOT/CLIP/...).
package sony

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fenixstarlord/replicant/internal/clips"
	"github.com/fenixstarlord/replicant/internal/extract"
	"github.com/fenixstarlord/replicant/internal/meta"
)

// Extractor parses Sony clip XML.
type Extractor struct{}

func (Extractor) Name() string                             { return "sony-xml" }
func (Extractor) Priority() int                            { return extract.PrioritySidecar }
func (Extractor) Available(context.Context) (bool, string) { return true, "builtin" }

func (Extractor) Matches(c *clips.Clip) bool {
	for _, p := range c.SidecarsWithExt("xml") {
		if strings.HasSuffix(strings.ToUpper(p), "M01.XML") {
			return true
		}
	}
	return false
}

func (Extractor) Extract(ctx context.Context, root string, c *clips.Clip) (*meta.Result, error) {
	var p string
	for _, s := range c.SidecarsWithExt("xml") {
		if strings.HasSuffix(strings.ToUpper(s), "M01.XML") {
			p = s
			break
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil {
		return nil, err
	}
	doc, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	rawJSON, _ := json.Marshal(doc)
	return &meta.Result{Fields: doc.Fields(), Raw: rawJSON}, nil
}

// Doc is the subset of NonRealTimeMeta we read.
type Doc struct {
	XMLName  xml.Name `xml:"NonRealTimeMeta" json:"-"`
	Duration struct {
		Value int64 `xml:"value,attr" json:"value"`
	} `xml:"Duration" json:"duration"`
	LtcChangeTable struct {
		TcFps   string `xml:"tcFps,attr" json:"tc_fps"`
		Changes []struct {
			FrameCount int64  `xml:"frameCount,attr" json:"frame_count"`
			Value      string `xml:"value,attr" json:"value"`
			Status     string `xml:"status,attr" json:"status"`
		} `xml:"LtcChange" json:"changes"`
	} `xml:"LtcChangeTable" json:"ltc_change_table"`
	CreationDate struct {
		Value string `xml:"value,attr" json:"value"`
	} `xml:"CreationDate" json:"creation_date"`
	VideoFormat struct {
		Frame struct {
			VideoCodec string `xml:"videoCodec,attr" json:"video_codec"`
			CaptureFps string `xml:"captureFps,attr" json:"capture_fps"`
			FormatFps  string `xml:"formatFps,attr" json:"format_fps"`
		} `xml:"VideoFrame" json:"frame"`
		Layout struct {
			Pixel             int64  `xml:"pixel,attr" json:"pixel"`
			NumOfVerticalLine int64  `xml:"numOfVerticalLine,attr" json:"lines"`
			AspectRatio       string `xml:"aspectRatio,attr" json:"aspect_ratio"`
		} `xml:"VideoLayout" json:"layout"`
	} `xml:"VideoFormat" json:"video_format"`
	AudioFormat struct {
		NumOfChannel int64 `xml:"numOfChannel,attr" json:"channels"`
		Ports        []struct {
			AudioCodec string `xml:"audioCodec,attr" json:"audio_codec"`
		} `xml:"AudioRecPort" json:"ports"`
	} `xml:"AudioFormat" json:"audio_format"`
	Device struct {
		Manufacturer string `xml:"manufacturer,attr" json:"manufacturer"`
		ModelName    string `xml:"modelName,attr" json:"model_name"`
		SerialNo     string `xml:"serialNo,attr" json:"serial_no"`
	} `xml:"Device" json:"device"`
	Lens struct {
		ModelName string `xml:"modelName,attr" json:"model_name"`
	} `xml:"Lens" json:"lens"`
	Groups []struct {
		Name  string `xml:"name,attr" json:"name"`
		Items []struct {
			Name  string `xml:"name,attr" json:"name"`
			Value string `xml:"value,attr" json:"value"`
		} `xml:"Item" json:"items"`
	} `xml:"AcquisitionRecord>Group" json:"acquisition_record"`
}

// Parse decodes the XML.
func Parse(raw []byte) (*Doc, error) {
	var d Doc
	if err := xml.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("sony xml: %w", err)
	}
	return &d, nil
}

func (d *Doc) item(group, name string) string {
	for _, g := range d.Groups {
		if group != "" && g.Name != group {
			continue
		}
		for _, it := range g.Items {
			if it.Name == name {
				return it.Value
			}
		}
	}
	return ""
}

// Fields maps the document to normalized metadata.
func (d *Doc) Fields() meta.Fields {
	var f meta.Fields
	f.CameraMake = meta.Str(d.Device.Manufacturer)
	f.CameraModel = meta.Str(d.Device.ModelName)
	f.CameraSerial = meta.Str(d.Device.SerialNo)
	f.Lens = meta.Str(d.Lens.ModelName)
	f.Codec = meta.Str(d.VideoFormat.Frame.VideoCodec)
	if d.VideoFormat.Layout.Pixel > 0 {
		f.Width = meta.Int(d.VideoFormat.Layout.Pixel)
		f.Height = meta.Int(d.VideoFormat.Layout.NumOfVerticalLine)
	}
	if fps := parseFps(d.VideoFormat.Frame.FormatFps); fps > 0 {
		f.FPS = meta.Float(fps)
	}
	if fps := parseFps(d.VideoFormat.Frame.CaptureFps); fps > 0 {
		f.CaptureFPS = meta.Float(fps)
	}
	if d.AudioFormat.NumOfChannel > 0 {
		f.AudioChannels = meta.Int(d.AudioFormat.NumOfChannel)
	}
	if len(d.AudioFormat.Ports) > 0 {
		// "LPCM24" -> 24 bit
		if n, err := strconv.ParseInt(strings.TrimLeft(d.AudioFormat.Ports[0].AudioCodec, "LPCM"), 10, 64); err == nil {
			f.AudioBitDepth = meta.Int(n)
		}
	}
	if t, err := time.Parse(time.RFC3339, d.CreationDate.Value); err == nil {
		f.RecordedAt = meta.Time(t.UTC())
	}
	tcFps := parseFps(d.LtcChangeTable.TcFps)
	if tcFps == 0 && f.FPS != nil {
		tcFps = *f.FPS
	}
	if len(d.LtcChangeTable.Changes) > 0 {
		f.TCStart = meta.Str(DecodeLTC(d.LtcChangeTable.Changes[0].Value))
		if last := d.LtcChangeTable.Changes[len(d.LtcChangeTable.Changes)-1]; last.Status == "end" {
			f.TCEnd = meta.Str(DecodeLTC(last.Value))
		}
	}
	if d.Duration.Value > 0 {
		f.FrameCount = meta.Int(d.Duration.Value)
		if tcFps > 0 {
			f.DurationS = meta.Float(float64(d.Duration.Value) / tcFps)
		}
	}
	if g := d.item("CameraUnitMetadataSet", "CaptureGammaEquation"); g != "" {
		if p := d.item("CameraUnitMetadataSet", "CaptureColorPrimaries"); p != "" {
			g = p + " / " + g
		}
		f.ColorGamma = meta.Str(g)
	}
	for _, key := range []string{"ISOSensitivity", "ExposureIndexOfPhotoMeter"} {
		if v := d.item("CameraUnitMetadataSet", key); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				f.ISO = meta.Int(n)
				break
			}
		}
	}
	if v := d.item("CameraUnitMetadataSet", "LightingPreset"); v != "" {
		if n, err := strconv.ParseInt(strings.TrimSuffix(v, "K"), 10, 64); err == nil {
			f.WBKelvin = meta.Int(n)
		}
	}
	if v := d.item("CameraUnitMetadataSet", "WhiteBalance"); v != "" && f.WBKelvin == nil {
		if n, err := strconv.ParseInt(strings.TrimSuffix(v, "K"), 10, 64); err == nil {
			f.WBKelvin = meta.Int(n)
		}
	}
	if v := d.item("CameraUnitMetadataSet", "ShutterSpeed_Angle"); v != "" {
		if x, err := strconv.ParseFloat(v, 64); err == nil {
			f.ShutterAngle = meta.Float(x)
		}
	}
	if v := d.item("CameraUnitMetadataSet", "ShutterSpeed_Time"); v != "" {
		f.ShutterSpeed = meta.Str(v)
	}
	if v := d.item("CameraUnitMetadataSet", "NeutralDensityFilterWheelSetting"); v != "" {
		f.ND = meta.Str(v)
	}
	if v := d.item("LensUnitMetadataSet", "FocalLength"); v != "" {
		if x, err := strconv.ParseFloat(v, 64); err == nil {
			f.FocalMM = meta.Float(x)
		}
	}
	if v := d.item("LensUnitMetadataSet", "IrisFNumber"); v != "" {
		if x, err := strconv.ParseFloat(v, 64); err == nil {
			f.TStop = meta.Float(x)
		}
	}
	if v := d.item("LensUnitMetadataSet", "FocusPositionFromImagePlane"); v != "" {
		f.FocusDistance = meta.Str(v)
	}
	if v := d.item("CameraUnitMetadataSet", "LookName"); v != "" {
		f.Look = meta.Str(v)
	}
	return f
}

// parseFps reads "23.98p", "25p", "59.94i", "24000/1001".
func parseFps(s string) float64 {
	s = strings.TrimRight(strings.TrimSpace(s), "pPiI")
	if num, den, ok := strings.Cut(s, "/"); ok {
		n, e1 := strconv.ParseFloat(num, 64)
		d, e2 := strconv.ParseFloat(den, 64)
		if e1 == nil && e2 == nil && d > 0 {
			return n / d
		}
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64)
	switch v {
	case 23.98:
		return 24000.0 / 1001
	case 29.97:
		return 30000.0 / 1001
	case 59.94:
		return 60000.0 / 1001
	}
	return v
}

// DecodeLTC turns Sony's "FFSSMMHH" byte-reversed BCD into HH:MM:SS:FF.
func DecodeLTC(v string) string {
	if len(v) != 8 {
		return v
	}
	return v[6:8] + ":" + v[4:6] + ":" + v[2:4] + ":" + v[0:2]
}
