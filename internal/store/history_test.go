package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiffBackupExport(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Two drives sharing one file, plus a changed file between scans.
	rootA, rootB := t.TempDir(), t.TempDir()
	write(t, rootA, "A001/A001C001.mov", "same-content")
	write(t, rootA, "A001/only_a.mov", "aaaa")
	write(t, rootB, "backup/A001C001.mov", "same-content")
	write(t, rootB, "backup/other.wav", "bbbbbb")
	t0 := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	ra, err := s.Ingest(ctx, makeBundle(t, rootA, t0))
	if err != nil {
		t.Fatal(err)
	}
	bB := makeBundle(t, rootB, t0)
	bB.Manifest.Volume.UUID, bB.Manifest.Volume.Name = "UUID-B", "B"
	if _, err := s.Ingest(ctx, bB); err != nil {
		t.Fatal(err)
	}

	// Second scan of A: one changed, one removed, one added.
	time.Sleep(5 * time.Millisecond)
	write(t, rootA, "A001/A001C001.mov", "same-content-but-longer")
	os.Remove(filepath.Join(rootA, "A001", "only_a.mov"))
	write(t, rootA, "A001/new.mov", "n")
	ra2, err := s.Ingest(ctx, makeBundle(t, rootA, t0.Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.DiffScans(ctx, ra.ScanID, ra2.ScanID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Added) != 1 || d.Added[0].Path != "A001/new.mov" || len(d.Removed) != 1 || d.Removed[0].Path != "A001/only_a.mov" || len(d.Changed) != 1 || d.Changed[0].Path != "A001/A001C001.mov" {
		t.Errorf("diff wrong: +%v -%v ~%v", d.Added, d.Removed, d.Changed)
	}
	if d.Changed[0].OldSize != 12 || d.Changed[0].NewSize != 23 {
		t.Errorf("sizes wrong: %+v", d.Changed[0])
	}
	// Reverse direction swaps added and removed.
	rd, _ := s.DiffScans(ctx, ra2.ScanID, ra.ScanID, 100)
	if len(rd.Added) != 1 || rd.Added[0].Path != "A001/only_a.mov" {
		t.Errorf("reverse diff wrong: %+v", rd.Added)
	}

	// Backup is a valid database with the same scans.
	bk := filepath.Join(t.TempDir(), BackupName(t0))
	if err := s.Backup(ctx, bk); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(bk)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	var n int
	if err := s2.DB.QueryRow(`SELECT count(*) FROM scans`).Scan(&n); err != nil || n != 3 {
		t.Errorf("backup scans = %d (%v)", n, err)
	}

	// Exports.
	clips, _, err := s.SearchClips(ctx, SearchQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var csvBuf, aleBuf bytes.Buffer
	if err := WriteCSV(&csvBuf, clips); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(csvBuf.String()), "\n")
	if len(lines) != len(clips)+1 || !strings.HasPrefix(lines[0], "drive,label,location,path,clip") {
		t.Errorf("csv wrong: %d lines, header %q", len(lines), lines[0])
	}
	if err := WriteALE(&aleBuf, clips); err != nil {
		t.Fatal(err)
	}
	ale := aleBuf.String()
	if !strings.HasPrefix(ale, "Heading\r\nFIELD_DELIM\tTABS\r\n") || !strings.Contains(ale, "\r\nColumn\r\nName\tTape\tSource File\t") || !strings.Contains(ale, "\r\nData\r\n") {
		t.Errorf("ale structure wrong: %.200q", ale)
	}
	if !strings.Contains(ale, "A001C001.mov") || strings.Count(ale, "\r\n") < 8+len(clips) {
		t.Errorf("ale rows missing: %q", ale)
	}
}
