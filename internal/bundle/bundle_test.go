package bundle

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/fenixstarlord/indexserver/internal/clips"
	"github.com/fenixstarlord/indexserver/internal/scan"
)

func TestRoundTrip(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	bt := now.Add(-time.Hour)
	entries := []scan.Entry{
		{Path: "A001", Name: "A001", Kind: scan.KindDir, IsDir: true, ModTime: now},
		{Path: "A001/A001C001.mov", Name: "A001C001.mov", Ext: "mov", Kind: scan.KindVideo, Size: 123, ModTime: now, BirthTime: &bt, Fingerprint: "00000000deadbeef", ClipID: "c1"},
	}
	cl := []clips.Clip{{ID: "c1", Kind: clips.KindFile, Name: "A001C001", RootPath: "A001/A001C001.mov", Files: []string{"A001/A001C001.mov"}, FileCount: 1, TotalSize: 123, ModTime: now}}
	m := Manifest{
		ScannerVersion: "test", ScannedAt: now, Root: "/Volumes/X",
		Volume:     scan.Volume{MountPoint: "/Volumes/X", UUID: "ABC", Name: "X", FSType: "hfs", TotalBytes: 10, FreeBytes: 5},
		Options:    Options{Fast: true, Fingerprint: true, Skip: []string{".DS_Store"}},
		Extractors: []ExtractorInfo{{Name: "ffprobe", Version: "8.0", Available: true}},
		Summary:    scan.Summarize(entries),
	}

	var buf bytes.Buffer
	if err := Write(&buf, m, entries, cl); err != nil {
		t.Fatal(err)
	}
	b, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if b.Manifest.BundleVersion != Version || b.Manifest.ClipCount != 1 || b.Manifest.Volume.UUID != "ABC" || b.Manifest.Extractors[0].Name != "ffprobe" {
		t.Errorf("manifest wrong: %+v", b.Manifest)
	}
	if !reflect.DeepEqual(b.Entries, entries) {
		t.Errorf("entries differ:\n got %+v\nwant %+v", b.Entries, entries)
	}
	if !reflect.DeepEqual(b.Clips, cl) {
		t.Errorf("clips differ:\n got %+v\nwant %+v", b.Clips, cl)
	}
}

func TestReadRejectsGarbageAndMissingParts(t *testing.T) {
	junk := []byte("not a zip")
	if _, err := Read(bytes.NewReader(junk), int64(len(junk))); err == nil {
		t.Error("expected error for non-zip input")
	}
}

func TestEmptyBundle(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, Manifest{ScannedAt: time.Now()}, nil, nil); err != nil {
		t.Fatal(err)
	}
	b, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Entries) != 0 || len(b.Clips) != 0 {
		t.Errorf("expected empty bundle, got %d entries %d clips", len(b.Entries), len(b.Clips))
	}
}
