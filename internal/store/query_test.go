package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fenixstarlord/replicant/internal/meta"
)

func seed(t *testing.T) (*Store, IngestResult) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	root := t.TempDir()
	write(t, root, "A001/A001C001.mov", "aaaa")
	write(t, root, "A001/A001C002.mov", "bbbbbb")
	write(t, root, "AUDIO/36AT01.WAV", "wav")
	write(t, root, "RED/A001_C001.RDC/A001_C001_001.R3D", "r3d")
	write(t, root, "notes.txt", "n")
	b := makeBundle(t, root, time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC))
	for i := range b.Clips {
		c := &b.Clips[i]
		switch c.Name {
		case "A001C001":
			c.Meta = &meta.Fields{Codec: meta.Str("ARRICORE"), Width: meta.Int(4608), Height: meta.Int(3164), FPS: meta.Float(25),
				CameraModel: meta.Str("ALEXA 35"), ISO: meta.Int(800), WBKelvin: meta.Int(5600), Lens: meta.Str("Zeiss Supreme 50"),
				TCStart: meta.Str("08:46:50:00"), DurationS: meta.Float(113), ColorGamma: meta.Str("LOG-C"), Reel: meta.Str("A001")}
		case "A001C002":
			c.Meta = &meta.Fields{Codec: meta.Str("ARRICORE"), Width: meta.Int(4608), FPS: meta.Float(25), CameraModel: meta.Str("ALEXA 35"),
				ISO: meta.Int(1600), Lens: meta.Str("Zeiss Supreme 85"), DurationS: meta.Float(10), Reel: meta.Str("A001")}
		case "36AT01":
			c.Meta = &meta.Fields{Codec: meta.Str("pcm_24"), Scene: meta.Str("36A"), Take: meta.Str("01"), AudioChannels: meta.Int(8)}
		}
	}
	r, err := s.Ingest(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	return s, r
}

func names(rows []ClipRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}

func TestSearchClips(t *testing.T) {
	s, r := seed(t)
	ctx := context.Background()

	all, total, err := s.SearchClips(ctx, SearchQuery{})
	if err != nil || total != 4 || len(all) != 4 {
		t.Fatalf("all: %d %v %v", total, names(all), err)
	}
	if all[0].DriveName != "X" || !all[0].IsLatest || all[0].DriveID != r.DriveID {
		t.Errorf("row joins wrong: %+v", all[0])
	}

	// Substring via trigram FTS, case-insensitive.
	rows, _, _ := s.SearchClips(ctx, SearchQuery{Text: "c002"})
	if len(rows) != 1 || rows[0].Name != "A001C002" {
		t.Errorf("fts search = %v", names(rows))
	}
	// Short text falls back to LIKE.
	rows, _, _ = s.SearchClips(ctx, SearchQuery{Text: "36"})
	if len(rows) != 1 || rows[0].Name != "36AT01" {
		t.Errorf("short search = %v", names(rows))
	}
	// Camera metadata is in the FTS index too.
	rows, _, _ = s.SearchClips(ctx, SearchQuery{Text: "alexa"})
	if len(rows) != 2 {
		t.Errorf("camera search = %v", names(rows))
	}
	// Filters: the plan's example "ISO 800 + 25 fps + ALEXA 35".
	rows, _, _ = s.SearchClips(ctx, SearchQuery{Camera: "ALEXA 35", FPS: 25, MinISO: 800, MaxISO: 800})
	if len(rows) != 1 || rows[0].Name != "A001C001" || *rows[0].Meta.Lens != "Zeiss Supreme 50" {
		t.Errorf("filter search = %v", names(rows))
	}
	rows, _, _ = s.SearchClips(ctx, SearchQuery{Lens: "supreme", MinDur: 100})
	if len(rows) != 1 || rows[0].Name != "A001C001" {
		t.Errorf("lens+duration = %v", names(rows))
	}
	rows, _, _ = s.SearchClips(ctx, SearchQuery{Kind: "r3d"})
	if len(rows) != 1 || rows[0].Name != "A001_C001" {
		t.Errorf("kind = %v", names(rows))
	}
	rows, _, _ = s.SearchClips(ctx, SearchQuery{Scene: "36A", Take: "01"})
	if len(rows) != 1 {
		t.Errorf("scene/take = %v", names(rows))
	}
	rows, _, _ = s.SearchClips(ctx, SearchQuery{Timecode: "46:50"})
	if len(rows) != 1 {
		t.Errorf("timecode = %v", names(rows))
	}
	rows, _, _ = s.SearchClips(ctx, SearchQuery{DriveIDs: []int64{r.DriveID + 99}})
	if len(rows) != 0 {
		t.Errorf("drive filter = %v", names(rows))
	}
	// Sorting and paging.
	rows, total, _ = s.SearchClips(ctx, SearchQuery{Sort: "iso", Desc: true, Limit: 1})
	if total != 4 || len(rows) != 1 || rows[0].Name != "A001C002" {
		t.Errorf("sort iso desc = %v (total %d)", names(rows), total)
	}
	rows, _, _ = s.SearchClips(ctx, SearchQuery{Sort: "iso", Desc: true, Limit: 1, Offset: 1})
	if len(rows) != 1 || rows[0].Name != "A001C001" {
		t.Errorf("offset = %v", names(rows))
	}
	// Unknown sort column is ignored, not injected.
	if _, _, err := s.SearchClips(ctx, SearchQuery{Sort: "c.id; DROP TABLE clips"}); err != nil {
		t.Errorf("bad sort errored: %v", err)
	}
	// FTS phrase with quotes does not break the query.
	if _, _, err := s.SearchClips(ctx, SearchQuery{Text: `a"b OR x`}); err != nil {
		t.Errorf("quoted text errored: %v", err)
	}
	f, err := s.Facets(ctx)
	if err != nil || len(f.Codecs) != 2 || len(f.Cameras) != 1 || f.Cameras[0] != "ALEXA 35" || len(f.Kinds) != 2 {
		t.Errorf("facets = %+v %v", f, err)
	}
}

func TestEntriesBrowseSearchDetail(t *testing.T) {
	s, r := seed(t)
	ctx := context.Background()
	top, err := s.ListDir(ctx, r.ScanID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 4 || !top[0].IsDir || top[0].Name != "A001" || top[3].Name != "notes.txt" {
		t.Errorf("top listing wrong: %+v", top)
	}
	if top[0].DirTotalSize != 10 || top[0].DirFileCount != 2 {
		t.Errorf("dir totals wrong: %+v", top[0])
	}
	red, _ := s.ListDir(ctx, r.ScanID, "RED")
	if len(red) != 1 || red[0].ClipID == 0 || red[0].ClipKind != "r3d" {
		t.Errorf("RDC dir should link to its clip: %+v", red)
	}

	// Two MOVs, the RDC folder, and its R3D segment all contain "c00".
	rows, total, err := s.SearchEntries(ctx, EntrySearchQuery{Text: "c00"})
	if err != nil || total != 4 || !rows[0].IsDir {
		t.Errorf("name search: %d %v (dirs first: %+v)", total, err, rows)
	}
	rows, total, _ = s.SearchEntries(ctx, EntrySearchQuery{Text: "A001/"})
	if total != 2 || !rows[0].IsLatest {
		t.Errorf("path search: %d %+v", total, rows)
	}
	rows, _, _ = s.SearchEntries(ctx, EntrySearchQuery{Ext: "wav"})
	if len(rows) != 1 || rows[0].Name != "36AT01.WAV" {
		t.Errorf("ext search: %+v", rows)
	}

	e, err := s.EntryByPath(ctx, r.ScanID, "A001/A001C001.mov")
	if err != nil || e.ClipID == 0 || e.Fingerprint == "" {
		t.Fatalf("entry by path: %+v %v", e, err)
	}
	c, raw, err := s.GetClip(ctx, e.ClipID)
	if err != nil || c.Name != "A001C001" || *c.Meta.CameraModel != "ALEXA 35" || len(c.Files) != 1 || raw == nil {
		t.Errorf("get clip: %+v %v", c, err)
	}
	hist, _ := s.ClipHistory(ctx, c.DriveID, c.RootPath)
	if len(hist) != 1 {
		t.Errorf("history = %d", len(hist))
	}
	copies, _ := s.Copies(ctx, e)
	if len(copies) != 0 {
		t.Errorf("unexpected copies: %+v", copies)
	}
	scans, _ := s.ListScans(ctx, r.DriveID)
	if len(scans) != 1 || !scans[0].IsLatest || scans[0].FileCount != 5 {
		t.Errorf("scans = %+v", scans)
	}
	if d, err := s.GetDrive(ctx, r.DriveID); err != nil || d.Name != "X" {
		t.Errorf("get drive: %+v %v", d, err)
	}
}
