package ale

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fenixstarlord/replicant/internal/clips"
	"github.com/fenixstarlord/replicant/internal/scan"
)

const fixture = "arri_alexa35_arricore.ale"

func fixturesDir(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "..", "testdata", "sidecars"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseAndMapARRI(t *testing.T) {
	f, err := os.Open(filepath.Join(fixturesDir(t), fixture))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	heading, rows, err := ParseReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if heading["FPS"] != "24" || heading["FIELD_DELIM"] != "TABS" || len(rows) < 2 {
		t.Fatalf("heading %v rows %d", heading, len(rows))
	}
	r := rows[0]
	if r["Name"] != "A_0001C001_260930_084650_c1BY9" || r["Camera_model"] != "ALEXA 35" {
		t.Fatalf("row wrong: %v", r)
	}
	m := MapRow(r, heading)
	checks := map[string]string{
		"clip_name": "A_0001C001_260930_084650_c1BY9", "tc_start": "00:00:00:00", "tc_end": "00:01:53:02",
		"codec": "ARRICORE", "sensor_mode": "3164p", "color_gamma": "LOG-C", "look": "Default.ALF4",
		"camera_make": "ARRI", "camera_model": "ALEXA 35", "camera_serial": "0062145", "camera_index": "A",
		"firmware": "6.01.00", "reel": "A_0001_1BY9", "lens": "Angenieux DETUNED 45-120 1",
	}
	got := map[string]string{
		"clip_name": *m.ClipName, "tc_start": *m.TCStart, "tc_end": *m.TCEnd, "codec": *m.Codec,
		"sensor_mode": *m.SensorMode, "color_gamma": *m.ColorGamma, "look": *m.Look, "camera_make": *m.CameraMake,
		"camera_model": *m.CameraModel, "camera_serial": *m.CameraSerial, "camera_index": *m.CameraIndex,
		"firmware": *m.Firmware, "reel": *m.Reel, "lens": *m.Lens,
	}
	for k, want := range checks {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	if *m.ISO != 800 || *m.WBKelvin != 5000 || *m.Tint != 0 || *m.ShutterAngle != 180 || *m.FPS != 24 || *m.CaptureFPS != 24 {
		t.Errorf("exposure wrong: iso=%d wb=%d tint=%v shutter=%v fps=%v sensor=%v", *m.ISO, *m.WBKelvin, *m.Tint, *m.ShutterAngle, *m.FPS, *m.CaptureFPS)
	}
	if *m.Width != 4608 || *m.Height != 3164 || *m.AudioChannels != 5 || *m.SampleRate != 48000 || *m.AudioBitDepth != 24 {
		t.Errorf("format wrong: %dx%d %dch %dHz %dbit", *m.Width, *m.Height, *m.AudioChannels, *m.SampleRate, *m.AudioBitDepth)
	}
	if *m.FrameCount != 113*24+2 || *m.DurationS < 113 || *m.DurationS > 113.1 {
		t.Errorf("duration wrong: %d frames %v s", *m.FrameCount, *m.DurationS)
	}
	if m.RecordedAt == nil || m.RecordedAt.Format("2006-01-02 15:04:05") != "2026-09-30 08:46:50" {
		t.Errorf("recorded_at = %v", m.RecordedAt)
	}
	if m.Scene != nil || m.Take != nil {
		t.Errorf("empty scene/take should stay nil: %v %v", m.Scene, m.Take)
	}
}

func TestExtractorMatchesClipsByFileName(t *testing.T) {
	root := fixturesDir(t)
	entries := []scan.Entry{{Path: fixture, Name: fixture, Ext: "ale", Kind: scan.KindSidecar}}
	e := NewFromEntries(root, entries)
	match := &clips.Clip{Kind: clips.KindFile, Name: "A_0001C002_260930_084905_c1BY9", RootPath: "A_0001C002_260930_084905_c1BY9.mxf", Files: []string{"A_0001C002_260930_084905_c1BY9.mxf"}}
	if !e.Matches(match) {
		t.Fatal("expected clip to match an ALE row")
	}
	res, err := e.Extract(context.Background(), root, match)
	if err != nil {
		t.Fatal(err)
	}
	if *res.Fields.TCStart != "00:01:53:02" || *res.Fields.CameraModel != "ALEXA 35" || len(res.Raw) == 0 {
		t.Errorf("extract wrong: %+v", res.Fields)
	}
	if e.Matches(&clips.Clip{Kind: clips.KindFile, Name: "nope", RootPath: "nope.mov", Files: []string{"nope.mov"}}) {
		t.Error("unrelated clip matched")
	}
	if NewFromEntries(root, nil).Matches(match) {
		t.Error("extractor with no ALE files should never match")
	}
}
