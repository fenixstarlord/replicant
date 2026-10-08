package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fenixstarlord/indexserver/internal/bundle"
	"github.com/fenixstarlord/indexserver/internal/clips"
	"github.com/fenixstarlord/indexserver/internal/scan"
)

func TestFTS5TrigramInMemory(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := CheckFTS5Trigram(context.Background(), s.DB); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateIsIdempotentAndUsesWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("schema_migrations rows = %d, err %v", n, err)
	}
	var mode string
	if err := s.DB.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode = %q (%v), want wal", mode, err)
	}
	s.Close()
	if s2, err := Open(path); err != nil {
		t.Fatal(err)
	} else {
		s2.Close()
	}
}

// makeBundle scans a temp tree and wraps it as a bundle for volume "X".
func makeBundle(t *testing.T, root string, scannedAt time.Time) *bundle.Bundle {
	t.Helper()
	opts := scan.DefaultOptions()
	entries, err := scan.Scan(context.Background(), root, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries, cl := clips.Group(entries, clips.Options{})
	return &bundle.Bundle{
		Manifest: bundle.Manifest{
			BundleVersion: bundle.Version, ScannerVersion: "test", ScannedAt: scannedAt, Root: root,
			Volume:  scan.Volume{MountPoint: root, UUID: "UUID-X", Name: "X", FSType: "apfs", TotalBytes: 1000, FreeBytes: 500},
			Summary: scan.Summarize(entries),
		},
		Entries: entries,
		Clips:   cl,
	}
}

func write(t *testing.T, root, rel string, data string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIngestAndDiff(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	root := t.TempDir()
	write(t, root, "A001/A001C001.mov", "aaaa")
	write(t, root, "A001/A001C001.ale", "ale")
	write(t, root, "A001/sub/deep.wav", "wav")
	write(t, root, "RED/A001_C001.RDC/A001_C001_001.R3D", "r3d1")
	write(t, root, "RED/A001_C001.RDC/A001_C001_002.R3D", "r3d2")
	write(t, root, "notes.txt", "n")

	t0 := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	r1, err := s.Ingest(ctx, makeBundle(t, root, t0))
	if err != nil {
		t.Fatal(err)
	}
	if !r1.FirstScan || !r1.IsLatest || r1.Files != 6 || r1.Clips != 3 || r1.Added != 6 || r1.Removed != 0 || r1.Changed != 0 {
		t.Errorf("first ingest wrong: %+v", r1)
	}

	// Directory aggregates.
	var size, files int64
	if err := s.DB.QueryRow(`SELECT dir_total_size, dir_file_count FROM entries WHERE scan_id = ? AND path = 'A001'`, r1.ScanID).Scan(&size, &files); err != nil {
		t.Fatal(err)
	}
	if size != 4+3+3 || files != 3 {
		t.Errorf("A001 aggregates = %d bytes / %d files, want 10 / 3", size, files)
	}
	var clipKey string
	if err := s.DB.QueryRow(`SELECT clip_key FROM entries WHERE scan_id = ? AND path = 'RED/A001_C001.RDC/A001_C001_001.R3D'`, r1.ScanID).Scan(&clipKey); err != nil || clipKey == "" {
		t.Errorf("R3D entry not linked to a clip: %q %v", clipKey, err)
	}
	// FTS over paths.
	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM entries e JOIN entries_fts f ON f.rowid = e.id
		WHERE entries_fts MATCH 'c001' AND e.scan_id = ?`, r1.ScanID).Scan(&n); err != nil || n != 5 {
		t.Errorf("entries_fts match = %d (%v), want 5 (mov, ale, RDC dir, two R3D)", n, err)
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM clips_fts WHERE clips_fts MATCH 'a001_c001' AND scan_id = ?`, r1.ScanID).Scan(&n); err != nil || n != 1 {
		t.Errorf("clips_fts match = %d (%v), want 1", n, err)
	}

	// Second scan: one changed, one removed, one added.
	time.Sleep(10 * time.Millisecond)
	write(t, root, "A001/A001C001.mov", "aaaaaa")
	os.Remove(filepath.Join(root, "notes.txt"))
	write(t, root, "A001/new.mov", "new")
	r2, err := s.Ingest(ctx, makeBundle(t, root, t0.Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if r2.FirstScan || !r2.IsLatest || r2.Added != 1 || r2.Removed != 1 || r2.Changed != 1 {
		t.Errorf("second ingest wrong: %+v", r2)
	}
	var latest int64
	if err := s.DB.QueryRow(`SELECT id FROM scans WHERE drive_id = ? AND is_latest = 1`, r1.DriveID).Scan(&latest); err != nil || latest != r2.ScanID {
		t.Errorf("latest scan = %d (%v), want %d", latest, err, r2.ScanID)
	}
	changes := map[string]string{}
	rows, err := s.DB.Query(`SELECT path, change FROM scan_changes WHERE scan_id = ?`, r2.ScanID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var p, c string
		rows.Scan(&p, &c)
		changes[p] = c
	}
	rows.Close()
	want := map[string]string{"A001/A001C001.mov": "changed", "notes.txt": "removed", "A001/new.mov": "added"}
	for p, c := range want {
		if changes[p] != c {
			t.Errorf("change[%s] = %q, want %q (all: %v)", p, changes[p], c, changes)
		}
	}

	// An older bundle uploaded later must not become latest.
	r3, err := s.Ingest(ctx, makeBundle(t, root, t0.Add(-time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if r3.IsLatest {
		t.Errorf("older scan should not be latest: %+v", r3)
	}
	drives, err := s.ListDrives(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(drives) != 1 || drives[0].ScanCount != 3 || drives[0].Name != "X" || drives[0].ClipCount != 4 {
		t.Errorf("drives wrong: %+v", drives)
	}
}

func TestIngestRollsBackOnError(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	write(t, root, "a.mov", "x")
	b := makeBundle(t, root, time.Now())
	b.Entries = append(b.Entries, b.Entries[0]) // duplicate path violates UNIQUE(scan_id, path)
	if _, err := s.Ingest(ctx, b); err == nil {
		t.Fatal("expected ingest to fail")
	}
	for _, table := range []string{"drives", "scans", "entries", "clips"} {
		var n int
		if err := s.DB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s has %d rows after failed ingest (%v)", table, n, err)
		}
	}
}

func TestTokens(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	plain, id, err := s.CreateToken(ctx, "laptop")
	if err != nil || id == 0 || len(plain) < 40 || plain[:len(TokenPrefix)] != TokenPrefix {
		t.Fatalf("create: %q %d %v", plain, id, err)
	}
	tok, ok, err := s.VerifyToken(ctx, plain)
	if err != nil || !ok || tok.Name != "laptop" {
		t.Errorf("verify: %+v %v %v", tok, ok, err)
	}
	if _, ok, _ := s.VerifyToken(ctx, plain+"x"); ok {
		t.Error("tampered token verified")
	}
	list, _ := s.ListTokens(ctx)
	if len(list) != 1 || list[0].LastUsedAt == nil {
		t.Errorf("list: %+v", list)
	}
	if gone, _ := s.RevokeToken(ctx, id); !gone {
		t.Error("revoke reported nothing deleted")
	}
	if _, ok, _ := s.VerifyToken(ctx, plain); ok {
		t.Error("revoked token still verifies")
	}
}
