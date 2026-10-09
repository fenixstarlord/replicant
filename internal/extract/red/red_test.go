package red

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fenixstarlord/replicant/internal/clips"
)

func TestParseAndMapVRaptor(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "golden", "red", "vraptor_x_printmeta1.txt"))
	if err != nil {
		t.Fatal(err)
	}
	kv := ParseKV(string(raw))
	if kv["Camera Model"] != "V-RAPTOR [X] 8K VV" || kv["LGG Lift: Green"] != "0" || kv["CDL Enabled"] != "false" {
		t.Fatalf("parse wrong: model=%q lgg=%q cdl=%q", kv["Camera Model"], kv["LGG Lift: Green"], kv["CDL Enabled"])
	}
	f := Map(kv)
	got := map[string]string{
		"clip_name": *f.ClipName, "reel": *f.Reel, "camera_index": *f.CameraIndex, "camera_model": *f.CameraModel,
		"camera_serial": *f.CameraSerial, "firmware": *f.Firmware, "tc_start": *f.TCStart, "tc_end": *f.TCEnd,
		"codec": *f.Codec, "codec_detail": *f.CodecDetail, "sensor_mode": *f.SensorMode, "shutter_speed": *f.ShutterSpeed,
		"nd": *f.ND, "lens": *f.Lens, "focus": *f.FocusDistance, "color": *f.ColorGamma, "take": *f.Take,
	}
	want := map[string]string{
		"clip_name": "A014_A002_05102I", "reel": "A014", "camera_index": "A", "camera_model": "V-RAPTOR [X] 8K VV",
		"camera_serial": "VRPBX000094", "firmware": "1.7.0", "tc_start": "16:31:28:18", "tc_end": "16:31:42:16",
		"codec": "R3D", "codec_detail": "REDCODE MQ", "sensor_mode": "8192x4320 V-RAPTOR [X] VV", "shutter_speed": "1/24",
		"nd": "3.67 stops", "lens": "Carl Zeiss AG Supreme Prime 100/T1.5", "focus": "2.19 m", "color": "REDWideGamutRGB/Log3G10", "take": "002",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if *f.Width != 8192 || *f.Height != 4320 || *f.FrameCount != 335 || *f.ISO != 250 || *f.WBKelvin != 5600 || *f.Tint != 0 || *f.ShutterAngle != 360.3 || *f.FocalMM != 100 || *f.TStop != 4.8 || *f.AudioChannels != 4 {
		t.Errorf("numbers wrong: %dx%d frames=%d iso=%d wb=%d tint=%v shutter=%v focal=%v tstop=%v ch=%d", *f.Width, *f.Height, *f.FrameCount, *f.ISO, *f.WBKelvin, *f.Tint, *f.ShutterAngle, *f.FocalMM, *f.TStop, *f.AudioChannels)
	}
	if fps := *f.FPS; fps < 23.97 || fps > 23.98 || *f.DurationS < 13.9 || *f.DurationS > 14.0 {
		t.Errorf("fps/duration wrong: %v %v", fps, *f.DurationS)
	}
	if f.RecordedAt == nil || f.RecordedAt.Format("2006-01-02 15:04:05") != "2024-05-10 02:36:39" {
		t.Errorf("recorded_at = %v", f.RecordedAt)
	}
	if f.Scene != nil || f.Circled != nil || f.Squeeze != nil || f.Look != nil {
		t.Errorf("empty values should stay nil: scene=%v circled=%v squeeze=%v look=%v", f.Scene, f.Circled, f.Squeeze, f.Look)
	}
}

func TestMatchesLooseSegments(t *testing.T) {
	var e Extractor
	if !e.Matches(&clips.Clip{Kind: clips.KindR3D, RootPath: "A.RDC", Files: []string{"A.RDC/A_001.R3D"}}) {
		t.Error("RDC clip should match")
	}
	if !e.Matches(&clips.Clip{Kind: clips.KindFile, RootPath: "x/A_001.R3D", Files: []string{"x/A_001.R3D"}}) {
		t.Error("loose R3D should match")
	}
	if e.Matches(&clips.Clip{Kind: clips.KindFile, RootPath: "x/a.mov", Files: []string{"x/a.mov"}}) {
		t.Error("mov should not match")
	}
}
