package arri

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMapExportALEXA35(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "golden", "arri", "alexa35_arricore_metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !IsExport(raw) {
		t.Fatal("fixture not recognised as an export")
	}
	var ex Export
	if err := json.Unmarshal(raw, &ex); err != nil {
		t.Fatal(err)
	}
	f := MapExport(&ex)
	want := map[string]string{
		"camera_make": *f.CameraMake, "camera_model": *f.CameraModel, "camera_serial": *f.CameraSerial, "firmware": *f.Firmware,
		"clip_name": *f.ClipName, "reel": *f.Reel, "camera_index": *f.CameraIndex, "codec": *f.Codec, "codec_detail": *f.CodecDetail,
		"color_gamma": *f.ColorGamma, "look": *f.Look, "lens": *f.Lens, "shutter_speed": *f.ShutterSpeed, "nd": *f.ND,
		"focus": *f.FocusDistance, "tc_start": *f.TCStart, "sensor_mode": *f.SensorMode,
	}
	expect := map[string]string{
		"camera_make": "ARRI", "camera_model": "ALEXA 35", "camera_serial": "62145", "firmware": "6.01.00",
		"clip_name": "A_0001C001_260930_084650_c1BY9", "reel": "A_0001_1BY9", "camera_index": "A", "codec": "ARRICORE",
		"codec_detail": "ARRICORE CBE Standard Profile", "color_gamma": "AWG4/LogC4", "look": "Default.ALF4",
		"lens": "Angenieux DETUNED 45-120 1", "shutter_speed": "1/48", "nd": "1.2", "focus": "11.34 m", "tc_start": "00:00:00:00",
		"sensor_mode": "4608x3164 ALEV IV 4:3",
	}
	for k, v := range expect {
		if want[k] != v {
			t.Errorf("%s = %q, want %q", k, want[k], v)
		}
	}
	if *f.Width != 4608 || *f.Height != 3164 || *f.FPS != 24 || *f.CaptureFPS != 24 || *f.ISO != 800 || *f.WBKelvin != 5000 || *f.Tint != 0 || *f.ShutterAngle != 180 {
		t.Errorf("numbers wrong: %dx%d fps=%v cap=%v iso=%d wb=%d tint=%v shutter=%v", *f.Width, *f.Height, *f.FPS, *f.CaptureFPS, *f.ISO, *f.WBKelvin, *f.Tint, *f.ShutterAngle)
	}
	if *f.FocalMM != 45.8 || f.TStop != nil || f.Squeeze != nil || *f.BitDepth != 13 {
		t.Errorf("lens/depth wrong: focal=%v tstop=%v squeeze=%v depth=%v", *f.FocalMM, f.TStop, f.Squeeze, *f.BitDepth)
	}
	if *f.AudioChannels != 2 || *f.SampleRate != 48000 || *f.AudioBitDepth != 24 || *f.Circled != false {
		t.Errorf("audio/circled wrong: %v", f.Set())
	}
	if f.RecordedAt == nil || f.RecordedAt.Format("2006-01-02T15:04:05Z") != "2026-09-30T08:46:50Z" {
		t.Errorf("recorded_at = %v", f.RecordedAt)
	}
	// --duration 1 truncates clipDuration, so duration and frame count must not come from here.
	if f.DurationS != nil || f.FrameCount != nil {
		t.Errorf("duration should be left to other sources: %v %v", f.DurationS, f.FrameCount)
	}
}
