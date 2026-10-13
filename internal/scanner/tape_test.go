package scanner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/fenixstarlord/replicant/internal/clips"
	"github.com/fenixstarlord/replicant/internal/scan"
)

func TestParseTapeMode(t *testing.T) {
	for in, want := range map[string]TapeMode{"": TapeAuto, "auto": TapeAuto, "ON": TapeOn, " off ": TapeOff} {
		if got, err := ParseTapeMode(in); err != nil || got != want {
			t.Errorf("ParseTapeMode(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseTapeMode("yes"); err == nil {
		t.Error("unknown mode should fail")
	}
	for _, tc := range []struct {
		mode TapeMode
		ltfs bool
		want bool
	}{{"", true, true}, {"", false, false}, {TapeAuto, true, true}, {TapeOn, false, true}, {TapeOff, true, false}} {
		if got := tc.mode.enabled(tc.ltfs); got != tc.want {
			t.Errorf("%q.enabled(%v) = %v", tc.mode, tc.ltfs, got)
		}
	}
}

// unitPaths renders a plan as "unit[file file]" strings for comparison.
func unitPaths(units []tapeUnit, entries []scan.Entry, cl []clips.Clip) []string {
	var out []string
	for _, u := range units {
		name := u.path
		if u.clip >= 0 {
			name = "clip:" + cl[u.clip].ID
		}
		var files []string
		for _, f := range u.files {
			p := entries[f.entry].Path
			if !f.hash {
				p = "~" + p // read by extractors only
			}
			files = append(files, p)
		}
		out = append(out, name+"["+strings.Join(files, " ")+"]")
	}
	return out
}

func TestPlanTapeOrdersByTapePosition(t *testing.T) {
	entries := []scan.Entry{
		{Path: "A", IsDir: true},
		{Path: "A/c1.mov", ClipID: "X", Size: 10},
		{Path: "A/c1.xml", ClipID: "X", Size: 1},
		{Path: "A/c2.mov", ClipID: "Y", Size: 10},
		{Path: "B/r.RDC", IsDir: true, ClipID: "Z"},
		{Path: "B/r.RDC/r_002.R3D", ClipID: "Z", Size: 10},
		{Path: "B/r.RDC/r_001.R3D", ClipID: "Z", Size: 10},
		{Path: "empty.txt"},
		{Path: "idx.xml", Size: 1},
		{Path: "link.mov", IsSymlink: true},
		{Path: "notes.txt", Size: 1},
		{Path: "unread.txt", Size: 1, Error: "permission denied"},
	}
	cl := []clips.Clip{
		{ID: "X", RootPath: "A/c1.mov", Files: []string{"A/c1.mov"}, Sidecars: []string{"A/c1.xml"}},
		{ID: "Y", RootPath: "A/c2.mov", Files: []string{"A/c2.mov"}},
		{ID: "Z", RootPath: "B/r.RDC", Files: []string{"B/r.RDC/r_001.R3D", "B/r.RDC/r_002.R3D"}},
		{ID: "W", RootPath: "D", FramePath: "D/f.0001.ari"}, // ARRIRAW, frames dropped
	}
	pos := map[string]scan.TapePosition{
		"A/c1.mov": {Partition: "b", Block: 500}, "A/c1.xml": {Partition: "b", Block: 510}, "A/c2.mov": {Partition: "b", Block: 100},
		"B/r.RDC/r_001.R3D": {Partition: "b", Block: 50}, "B/r.RDC/r_002.R3D": {Partition: "b", Block: 60},
		"D/f.0001.ari": {Partition: "b", Block: 200}, "idx.xml": {Partition: "a", Block: 10}, "notes.txt": {Partition: "b", Block: 300},
	}
	var asked []string
	lookup := func(rel string) (scan.TapePosition, bool) {
		asked = append(asked, rel)
		p, ok := pos[rel]
		return p, ok
	}

	got := unitPaths(planTape(entries, cl, true, true, lookup), entries, cl)
	want := []string{
		"idx.xml[idx.xml]",
		"clip:Z[B/r.RDC/r_001.R3D B/r.RDC/r_002.R3D]",
		"clip:Y[A/c2.mov]",
		"clip:W[]",
		"notes.txt[notes.txt]",
		"clip:X[A/c1.mov A/c1.xml]",
		"empty.txt[empty.txt]", // no extents, so no position: after everything with one
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hash+extract plan:\n got %q\nwant %q", got, want)
	}
	for _, p := range asked {
		if p == "A" || p == "link.mov" || p == "unread.txt" {
			t.Errorf("looked up a position for %s, which is never read", p)
		}
	}

	got = unitPaths(planTape(entries, cl, true, false, lookup), entries, cl)
	want = []string{"idx.xml[idx.xml]", "clip:Z[B/r.RDC/r_001.R3D B/r.RDC/r_002.R3D]", "clip:Y[A/c2.mov]",
		"notes.txt[notes.txt]", "clip:X[A/c1.mov A/c1.xml]", "empty.txt[empty.txt]"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hash-only plan:\n got %q\nwant %q", got, want)
	}

	got = unitPaths(planTape(entries, cl, false, true, lookup), entries, cl)
	want = []string{"clip:Z[~B/r.RDC/r_001.R3D ~B/r.RDC/r_002.R3D]", "clip:Y[~A/c2.mov]", "clip:W[]", "clip:X[~A/c1.mov ~A/c1.xml]"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extract-only plan:\n got %q\nwant %q", got, want)
	}

	none := func(string) (scan.TapePosition, bool) { return scan.TapePosition{}, false }
	got = unitPaths(planTape(entries, cl, true, true, none), entries, cl)
	want = []string{"clip:X[A/c1.mov A/c1.xml]", "clip:Y[A/c2.mov]", "clip:Z[B/r.RDC/r_001.R3D B/r.RDC/r_002.R3D]",
		"clip:W[]", "empty.txt[empty.txt]", "idx.xml[idx.xml]", "notes.txt[notes.txt]"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("no positions should fall back to path order:\n got %q\nwant %q", got, want)
	}
}

// buildMediaTree writes a small card-like tree whose files straddle the
// fingerprint and stream-limit boundaries.
func buildMediaTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]int{
		"A001/A001C001.mov":       5 << 20,
		"A001/A001C001.xml":       200,
		"A001/A001C002.mov":       3 << 20,
		"RED/A001_C001.RDC/a.R3D": 4 << 20,
		"RED/A001_C001.RDC/b.R3D": 1 << 20,
		"AUDIO/T01.wav":           2 << 20,
		"notes.txt":               5,
		"empty.txt":               0,
	}
	seed := byte(1)
	for rel, n := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, bytes.Repeat([]byte{seed, seed * 3, 7}, n/3+1)[:n], 0o644); err != nil {
			t.Fatal(err)
		}
		seed++
	}
	return root
}

func TestRunTapeMatchesDiskScan(t *testing.T) {
	root := buildMediaTree(t)

	// Fake LTFS positions in reverse path order, so tape order differs
	// from the order a disk scan uses.
	var paths []string
	filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() && fi.Size() > 0 {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	fake := map[string]scan.TapePosition{}
	for i, p := range paths {
		fake[p] = scan.TapePosition{Partition: "b", Block: int64(100 * (i + 1))}
	}
	t.Cleanup(func() { readTapePosition = scan.ReadTapePosition })
	readTapePosition = func(p string) (scan.TapePosition, bool) {
		pos, ok := fake[p]
		return pos, ok
	}

	base := Options{Fingerprint: true, FullHash: true, StreamLimit: 4 << 20, Version: "test"}
	disk := base
	disk.Tape = TapeOff
	want, err := Run(context.Background(), root, disk, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want.Tape != nil {
		t.Fatal("tape stats on a disk scan")
	}

	for _, fullHash := range []bool{true, false} {
		tape := base
		tape.Tape, tape.FullHash = TapeOn, fullHash
		var stages []string
		got, err := Run(context.Background(), root, tape, func(p Progress) {
			if len(stages) == 0 || stages[len(stages)-1] != p.Stage {
				stages = append(stages, p.Stage)
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Entries) != len(want.Entries) || len(got.Clips) != len(want.Clips) {
			t.Fatalf("tape scan has %d entries/%d clips, disk %d/%d", len(got.Entries), len(got.Clips), len(want.Entries), len(want.Clips))
		}
		for i := range want.Entries {
			w, g := want.Entries[i], got.Entries[i]
			if g.Path != w.Path || g.Fingerprint != w.Fingerprint || g.ClipID != w.ClipID || g.Error != w.Error {
				t.Errorf("entry %s: tape %+v, disk %+v", w.Path, g, w)
			}
			if fullHash && g.FullHash != w.FullHash {
				t.Errorf("entry %s: tape full hash %q, disk %q", w.Path, g.FullHash, w.FullHash)
			}
		}
		for i := range want.Clips {
			if got.Clips[i].ID != want.Clips[i].ID {
				t.Errorf("clip %d: %s vs %s", i, got.Clips[i].ID, want.Clips[i].ID)
			}
		}

		st := got.Tape
		if st == nil {
			t.Fatal("no tape stats")
		}
		// 8 files, 7 with data; empty.txt has no position.
		if st.Files != 8 || st.Positioned != 7 || st.Backward != 0 || st.StreamLimit != 4<<20 {
			t.Errorf("stats = %+v", *st)
		}
		wantStreamed, wantHeadTail := 8, 0
		if !fullHash {
			wantStreamed, wantHeadTail = 7, 1 // the 5 MiB .mov is over the 4 MiB limit
		}
		if st.Streamed != wantStreamed || st.HeadTail != wantHeadTail {
			t.Errorf("streamed %d head+tail %d, want %d and %d", st.Streamed, st.HeadTail, wantStreamed, wantHeadTail)
		}
		if st.ClipTimeoutS != int64(TapeClipTimeout.Seconds()) {
			t.Errorf("clip timeout %d s, want the tape default", st.ClipTimeoutS)
		}
		// One pass: extraction and hashing alternate clip by clip.
		joined := strings.Join(stages, " ")
		if !strings.HasPrefix(joined, "walk tape ") || !strings.Contains(joined, "hash extract") {
			t.Errorf("stages = %v, want walk, tape, then extract and hash interleaved", stages)
		}
		m := got.Manifest()
		if !m.Options.Tape || m.Tape == nil {
			t.Errorf("manifest does not record tape mode: %+v", m.Options)
		}
		ran := false
		for _, x := range m.Extractors {
			ran = ran || x.Runs > 0
		}
		if !ran {
			t.Error("manifest has no extractor timings")
		}
	}
}

func TestRunAutoTapeModeOnlyOnLTFS(t *testing.T) {
	root := buildMediaTree(t)
	res, err := Run(context.Background(), root, Options{Fast: true, Fingerprint: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Volume.LTFS || res.Tape != nil {
		t.Errorf("a temp dir is not LTFS, but tape mode ran: %+v", res.Volume)
	}
}

// fakePositions makes readTapePosition answer from a map of paths
// relative to root, for the duration of the test.
func fakePositions(t *testing.T, root string, pos map[string]int64) {
	t.Helper()
	t.Cleanup(func() { readTapePosition = scan.ReadTapePosition })
	readTapePosition = func(p string) (scan.TapePosition, bool) {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return scan.TapePosition{}, false
		}
		b, ok := pos[filepath.ToSlash(rel)]
		return scan.TapePosition{Partition: "b", Block: b}, ok
	}
}

// Each extractor runs when the tape reaches the file it reads, whether a
// sidecar lies after its media (Sony) or before it (BRAW), so no read goes
// back on tape.
func TestRunTapeRunsExtractorsAtTheirFile(t *testing.T) {
	root := t.TempDir()
	files := map[string]int64{
		"XDROOT/Clip/C0001.MXF":    100,
		"XDROOT/Clip/C0001M01.XML": 200,
		"BRAW/A001.sidecar":        300,
		"BRAW/A001.braw":           400,
	}
	for rel := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, bytes.Repeat([]byte("x"), 3<<20), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fakePositions(t, root, files)
	var reads []string
	traceTapeRead = func(what, rel string) { reads = append(reads, what+" "+rel) }
	t.Cleanup(func() { traceTapeRead = nil })
	res, err := Run(context.Background(), root, Options{Fingerprint: true, Tape: TapeOn}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// ffprobe may be missing (CI); then it is "not available" and never runs.
	var got []string
	for _, r := range reads {
		if r != "extract XDROOT/Clip/C0001.MXF" && r != "extract BRAW/A001.braw" {
			got = append(got, r)
		}
	}
	want := []string{
		"hash XDROOT/Clip/C0001.MXF",
		"extract XDROOT/Clip/C0001M01.XML", "hash XDROOT/Clip/C0001M01.XML",
		"extract BRAW/A001.sidecar", "hash BRAW/A001.sidecar",
		"hash BRAW/A001.braw",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reads out of tape order:\n got %q\nwant %q (all: %q)", got, want, reads)
	}
	for _, media := range []string{"XDROOT/Clip/C0001.MXF", "BRAW/A001.braw"} {
		if i := slices.Index(reads, "extract "+media); i >= 0 && (i+1 >= len(reads) || reads[i+1] != "hash "+media) {
			t.Errorf("ffprobe on %s should run just before it is hashed: %q", media, reads)
		}
	}
	if res.Tape.Backward != 0 || res.Tape.Files != 4 || res.Tape.Positioned != 4 {
		t.Errorf("stats = %+v", *res.Tape)
	}
	runs := map[string]int{}
	for _, tm := range res.ExtractorTimes {
		runs[tm.Name] = tm.Runs
	}
	if runs["sony-xml"] != 1 || runs["braw-sidecar"] != 1 {
		t.Errorf("sidecar extractors did not run once each: %v", runs)
	}
	for _, c := range res.Clips {
		if c.Meta == nil {
			t.Errorf("clip %s was never finished", c.Name)
		}
	}
}

func TestRunTapeStopsOnCancel(t *testing.T) {
	root := t.TempDir()
	pos := map[string]int64{}
	for i := range 6 {
		rel := fmt.Sprintf("RED/A001_C001.RDC/A001_C001_%03d.R3D", i+1)
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, bytes.Repeat([]byte{byte(i)}, 3<<20), 0o644); err != nil {
			t.Fatal(err)
		}
		pos[rel] = int64(100 * (i + 1))
	}
	fakePositions(t, root, pos)
	ctx, cancel := context.WithCancel(context.Background())
	hashed := 0
	_, err := Run(ctx, root, Options{Fast: true, Fingerprint: true, Tape: TapeOn, StreamLimit: -1}, func(p Progress) {
		if p.Stage == "hash" {
			hashed++
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || hashed != 1 {
		t.Errorf("err = %v after %d files, want context.Canceled after 1", err, hashed)
	}
}
