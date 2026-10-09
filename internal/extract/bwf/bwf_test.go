package bwf

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fenixstarlord/replicant/internal/clips"
)

func TestParseSoundDevices(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", "..", "..", "testdata", "golden", "bwf"))
	f, err := os.Open(filepath.Join(root, "sounddevices_1.wav"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	if info.Fmt.Channels != 8 || info.Fmt.SampleRate != 48000 || info.Fmt.BitsPerSample != 24 {
		t.Errorf("fmt wrong: %+v", info.Fmt)
	}
	if info.Bext == nil || info.Bext.Originator != "Sound Dev: Mix688 S#NR0316141002" || info.Bext.OriginationDate != "2026-09-30" || info.Bext.TimeReference != 3420144001 {
		t.Fatalf("bext wrong: %+v", info.Bext)
	}
	if info.IXML == nil {
		t.Fatalf("iXML not parsed: %.300s", info.IXMLText)
	}
	ix := info.IXML
	if ix.Scene != "36A" || ix.Take != "01" || ix.Tape != "260930" || ix.Speed.TimecodeRate == "" {
		t.Errorf("ixml wrong: %+v", ix)
	}
	var boom bool
	for _, tr := range ix.Tracks {
		if tr.Name == "BOOM" {
			boom = true
		}
	}
	if !boom {
		t.Errorf("expected a BOOM track: %+v", ix.Tracks)
	}

	m := info.Fields()
	if *m.Scene != "36A" || *m.Take != "01" || *m.Reel != "260930" || *m.Circled != false || *m.FPS != 24 {
		t.Errorf("fields wrong: scene=%s take=%s reel=%s fps=%v", *m.Scene, *m.Take, *m.Reel, *m.FPS)
	}
	if *m.TCStart != "19:47:33:00" {
		t.Errorf("tc_start = %s", *m.TCStart)
	}
	if m.RecordedAt == nil || m.RecordedAt.Format("2006-01-02 15:04:05") != "2026-09-30 21:45:58" {
		t.Errorf("recorded_at = %v", m.RecordedAt)
	}
	if *m.AudioChannels != 8 || *m.SampleRate != 48000 || *m.AudioBitDepth != 24 || *m.Codec != "pcm_24" {
		t.Errorf("audio wrong: %v", m.Set())
	}
	if m.DurationS != nil {
		t.Errorf("carved fixture has no data; duration should be nil, got %v", *m.DurationS)
	}
}

func TestExtractorMatchesAndRuns(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", "..", "..", "testdata", "golden", "bwf"))
	c := &clips.Clip{Kind: clips.KindFile, Name: "sounddevices_2", RootPath: "sounddevices_2.wav", Files: []string{"sounddevices_2.wav"}}
	var e Extractor
	if !e.Matches(c) || e.Matches(&clips.Clip{Files: []string{"x.mov"}}) {
		t.Fatal("matching wrong")
	}
	res, err := e.Extract(context.Background(), root, c)
	if err != nil {
		t.Fatal(err)
	}
	if res.Fields.Scene == nil || len(res.Raw) == 0 {
		t.Errorf("extract wrong: %+v", res.Fields)
	}
}

func TestSamplesToTimecode(t *testing.T) {
	cases := []struct {
		samples uint64
		sr      int64
		fps     float64
		want    string
	}{
		{0, 48000, 24, "00:00:00:00"},
		{48000, 48000, 24, "00:00:01:00"},
		{48000 + 2000, 48000, 24, "00:00:01:01"},
		{3420144001, 48000, 24, "19:47:33:00"},
		{uint64(25*3600*48000) + 1, 48000, 25, "01:00:00:00"},
	}
	for _, c := range cases {
		if got := SamplesToTimecode(c.samples, c.sr, c.fps); got != c.want {
			t.Errorf("SamplesToTimecode(%d) = %s, want %s", c.samples, got, c.want)
		}
	}
}
