package ffprobe

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fenixstarlord/indexserver/internal/meta"
)

func golden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "golden", "ffprobe", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func str(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func i64(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

func f64(p *float64) float64 {
	if p == nil {
		return -1
	}
	return *p
}

func TestParseBRAW(t *testing.T) {
	r, err := Parse(golden(t, "braw.json"), "A001.braw")
	if err != nil {
		t.Fatal(err)
	}
	f := r.Fields
	if str(f.Container) != "braw" || str(f.Codec) != "BRAW" || i64(f.Width) != 6176 || i64(f.Height) != 3472 {
		t.Errorf("format wrong: %s %s %dx%d", str(f.Container), str(f.Codec), i64(f.Width), i64(f.Height))
	}
	if fps := f64(f.FPS); fps < 23.97 || fps > 23.98 {
		t.Errorf("fps = %v", fps)
	}
	if str(f.TCStart) != "16:08:39:13" || i64(f.FrameCount) != 185 || str(f.TCEnd) != "16:08:47:06" {
		t.Errorf("timecode wrong: %s -> %s (%d frames)", str(f.TCStart), str(f.TCEnd), i64(f.FrameCount))
	}
	if i64(f.AudioChannels) != 2 || i64(f.SampleRate) != 48000 || i64(f.AudioBitDepth) != 24 {
		t.Errorf("audio wrong: %d ch %d Hz %d bit", i64(f.AudioChannels), i64(f.SampleRate), i64(f.AudioBitDepth))
	}
	if f.RecordedAt == nil || f.RecordedAt.Year() != 2026 || f.RecordedAt.Month() != time.August {
		t.Errorf("recorded_at = %v", f.RecordedAt)
	}
	if d := f64(f.DurationS); str(f.Reel) != "1" || d < 7.7 || d > 7.8 {
		t.Errorf("reel/duration wrong: %s %v", str(f.Reel), d)
	}
	if len(r.Raw) < 1000 {
		t.Errorf("raw output not kept")
	}
}

func TestParseCanonCRM(t *testing.T) {
	r, err := Parse(golden(t, "canon_crm.json"), "A001C001_25081423_CANON.CRM")
	if err != nil {
		t.Fatal(err)
	}
	f := r.Fields
	if str(f.Codec) != "Canon RAW" || i64(f.Width) != 4096 || i64(f.Height) != 2160 || str(f.TCStart) != "11:01:13:04" || str(f.Container) != "crm" {
		t.Errorf("crm wrong: %+v", f.Set())
	}
	if i64(f.AudioChannels) != 1 || i64(f.AudioBitDepth) != 24 {
		t.Errorf("audio wrong: %d ch %d bit", i64(f.AudioChannels), i64(f.AudioBitDepth))
	}
}

func TestParseMP4(t *testing.T) {
	r, err := Parse(golden(t, "mp4.json"), "x.mp4")
	if err != nil {
		t.Fatal(err)
	}
	f := r.Fields
	if str(f.Codec) != "h264" || str(f.CodecDetail) != "High yuv420p" || i64(f.Width) != 1280 || f64(f.FPS) != 24 || i64(f.BitDepth) != 8 || str(f.TCStart) != "14:59:42:14" || i64(f.FrameCount) != 2458 {
		t.Errorf("mp4 wrong: codec=%s detail=%s w=%d fps=%v bd=%d tc=%s frames=%d", str(f.Codec), str(f.CodecDetail), i64(f.Width), f64(f.FPS), i64(f.BitDepth), str(f.TCStart), i64(f.FrameCount))
	}
	if str(f.Container) != "mp4" {
		t.Errorf("container = %s", str(f.Container))
	}
}

func TestParseWAVAndARRIMXF(t *testing.T) {
	r, err := Parse(golden(t, "bwf_wav.json"), "36AT01.WAV")
	if err != nil {
		t.Fatal(err)
	}
	f := r.Fields
	if str(f.Codec) != "pcm_s24le" || i64(f.AudioChannels) != 8 || i64(f.SampleRate) != 48000 || i64(f.AudioBitDepth) != 24 || f.Width != nil {
		t.Errorf("wav wrong: %v", f.Set())
	}
	r, err = Parse(golden(t, "arri_mxf.json"), "A_0003C001.mxf")
	if err != nil {
		t.Fatal(err)
	}
	f = r.Fields
	// ffprobe cannot read the ARRICORE descriptor: no video fields, but timecode and audio.
	if f.Width != nil || f.Codec != nil || str(f.Container) != "mxf" || str(f.TCStart) != "09:57:48:04" || i64(f.AudioChannels) != 1 {
		t.Errorf("arri mxf wrong: %v tc=%s", f.Set(), str(f.TCStart))
	}
}

func TestAddFrames(t *testing.T) {
	cases := []struct {
		tc   string
		n    int64
		fps  float64
		want string
	}{
		{"00:00:00:00", 24, 24, "00:00:01:00"},
		{"23:59:59:23", 1, 24, "00:00:00:00"},
		{"16:08:39:13", 185, 23.976, "16:08:47:06"},
		{"01:00:00:00", 90000, 25, "02:00:00:00"},
	}
	for _, c := range cases {
		got, ok := AddFrames(c.tc, c.n, c.fps)
		if !ok || got != c.want {
			t.Errorf("AddFrames(%s,%d,%v) = %s %v, want %s", c.tc, c.n, c.fps, got, ok, c.want)
		}
	}
	if _, ok := AddFrames("01:00:00;00", 1, 29.97); ok {
		t.Error("drop-frame should be refused")
	}
}

func TestAvailable(t *testing.T) {
	e := &Extractor{}
	ok, ver := e.Available(context.Background())
	if !ok {
		t.Skip("ffprobe not installed")
	}
	if ver == "" {
		t.Error("version empty")
	}
	if ok, _ := (&Extractor{Path: "/nonexistent/ffprobe"}).Available(context.Background()); ok {
		t.Error("bogus path reported available")
	}
	_ = meta.Fields{}
}
