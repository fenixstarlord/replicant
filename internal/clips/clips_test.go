package clips

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fenixstarlord/indexserver/internal/scan"
)

func touch(t *testing.T, root, rel string, size int) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func cardTree(t *testing.T) string {
	root := t.TempDir()
	// RED
	touch(t, root, "RED/A001_C001_0101AB.RDC/A001_C001_0101AB_001.R3D", 100)
	touch(t, root, "RED/A001_C001_0101AB.RDC/A001_C001_0101AB_002.R3D", 100)
	touch(t, root, "RED/A001_C001_0101AB.RDC/A001_C001_0101AB.RMD", 10)
	touch(t, root, "RED/EMPTY.RDC/EMPTY.RMD", 10)
	// ARRIRAW legacy sequence plus an unrelated file in the same folder
	for i := 1; i <= 5; i++ {
		touch(t, root, fmt.Sprintf("ARRI/A003C002_230101_R1ZX/A003C002_230101_R1ZX.%07d.ari", i), 20)
	}
	touch(t, root, "ARRI/A003C002_230101_R1ZX/notes.txt", 5)
	// Sony card
	touch(t, root, "SONY/XDROOT/Clip/C0001.MXF", 300)
	touch(t, root, "SONY/XDROOT/Clip/C0001M01.XML", 3)
	touch(t, root, "SONY/XDROOT/Clip/C0002.MXF", 300)
	touch(t, root, "SONY/XDROOT/MEDIAPRO.XML", 2)
	// BRAW with sidecar, loose files with and without sidecars, a non-media file
	touch(t, root, "BMD/B001_01011200_C001.braw", 400)
	touch(t, root, "BMD/B001_01011200_C001.sidecar", 4)
	touch(t, root, "loose/C.mov", 50)
	touch(t, root, "loose/c.xml", 1)
	touch(t, root, "loose/D.wav", 60)
	touch(t, root, "loose/photo.jpg", 7)
	return root
}

func byName(clips []Clip) map[string]Clip {
	m := map[string]Clip{}
	for _, c := range clips {
		m[c.Name] = c
	}
	return m
}

func TestGroup(t *testing.T) {
	root := cardTree(t)
	entries, err := scan.Walk(context.Background(), root, scan.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	out, clips := Group(entries, Options{})
	m := byName(clips)

	if len(clips) != 7 {
		t.Fatalf("got %d clips, want 7: %+v", len(clips), clips)
	}
	r3d := m["A001_C001_0101AB"]
	if r3d.Kind != KindR3D || len(r3d.Files) != 2 || len(r3d.Sidecars) != 1 || r3d.FileCount != 3 || r3d.TotalSize != 210 || r3d.RootPath != "RED/A001_C001_0101AB.RDC" {
		t.Errorf("r3d clip wrong: %+v", r3d)
	}
	if _, ok := m["EMPTY"]; ok {
		t.Errorf("an RDC folder with no R3D should not become a clip")
	}
	ari := m["A003C002_230101_R1ZX"]
	if ari.Kind != KindARRIRAW || ari.FrameCount != 5 || ari.FirstFrame != 1 || ari.LastFrame != 5 || ari.TotalSize != 100 || ari.FramePath != "ARRI/A003C002_230101_R1ZX/A003C002_230101_R1ZX.0000001.ari" {
		t.Errorf("arriraw clip wrong: %+v", ari)
	}
	if len(ari.Files) != 0 {
		t.Errorf("frames should not be listed when KeepFrames is false: %v", ari.Files)
	}
	c1 := m["C0001"]
	if c1.Kind != KindSony || len(c1.Sidecars) != 1 || c1.Sidecars[0] != "SONY/XDROOT/Clip/C0001M01.XML" {
		t.Errorf("sony clip wrong: %+v", c1)
	}
	if c2 := m["C0002"]; c2.Kind != KindSony || len(c2.Sidecars) != 0 {
		t.Errorf("sony clip without xml wrong: %+v", c2)
	}
	if b := m["B001_01011200_C001"]; b.Kind != KindBRAW || len(b.Sidecars) != 1 {
		t.Errorf("braw clip wrong: %+v", b)
	}
	if c := m["C"]; c.Kind != KindFile || len(c.Sidecars) != 1 || c.Sidecars[0] != "loose/c.xml" {
		t.Errorf("mov clip wrong: %+v", c)
	}
	if d := m["D"]; d.Kind != KindFile || len(d.Sidecars) != 0 {
		t.Errorf("wav clip wrong: %+v", d)
	}

	// Entries: frames dropped, members tagged, non-media untouched.
	for _, e := range out {
		switch {
		case e.Ext == "ari":
			t.Errorf("frame entry should have been dropped: %s", e.Path)
		case e.Path == "RED/A001_C001_0101AB.RDC":
			if e.ClipID != r3d.ID {
				t.Errorf("RDC dir not tagged with clip id: %+v", e)
			}
		case e.Path == "RED/EMPTY.RDC/EMPTY.RMD", e.Path == "loose/photo.jpg", e.Path == "SONY/XDROOT/MEDIAPRO.XML", e.Path == "ARRI/A003C002_230101_R1ZX/notes.txt":
			if e.ClipID != "" {
				t.Errorf("%s should not belong to a clip: %s", e.Path, e.ClipID)
			}
		case e.Path == "loose/c.xml":
			if e.ClipID != m["C"].ID {
				t.Errorf("sidecar not tagged: %+v", e)
			}
		}
	}

	// IDs are stable across runs and clips are sorted by root path.
	_, again := Group(entries, Options{})
	for i := range clips {
		if clips[i].ID != again[i].ID || clips[i].ID == "" {
			t.Errorf("unstable clip id at %d", i)
		}
	}
	roots := make([]string, len(clips))
	for i, c := range clips {
		roots[i] = c.RootPath
	}
	if !slices.IsSorted(roots) {
		t.Errorf("clips not sorted by root path: %v", roots)
	}
}

func TestGroupKeepFrames(t *testing.T) {
	root := cardTree(t)
	entries, err := scan.Walk(context.Background(), root, scan.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	out, clips := Group(entries, Options{KeepFrames: true})
	ari := byName(clips)["A003C002_230101_R1ZX"]
	if len(ari.Files) != 5 {
		t.Errorf("expected 5 frame files listed: %+v", ari)
	}
	var frames int
	for _, e := range out {
		if e.Ext == "ari" {
			frames++
			if e.ClipID != ari.ID {
				t.Errorf("frame not tagged: %+v", e)
			}
		}
	}
	if frames != 5 {
		t.Errorf("expected 5 frame entries kept, got %d", frames)
	}
}

func TestIsSonyClipDir(t *testing.T) {
	cases := map[string]bool{
		"SONY/XDROOT/Clip": true, "x/PRIVATE/M4ROOT/CLIP": true, "XDROOT/Clip": true,
		"XDROOT": false, "Clip": false, "SONY/XDROOT/Sub": false, "": false,
	}
	for d, want := range cases {
		if got := isSonyClipDir(d); got != want {
			t.Errorf("isSonyClipDir(%q) = %v, want %v", d, got, want)
		}
	}
}
