package scan

import (
	"context"
	"encoding/binary"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cespare/xxhash/v2"
)

func randBytes(n int, seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, seed^0xdeadbeef))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.UintN(256))
	}
	return b
}

func write(t *testing.T, root, rel string, data []byte) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// buildTree creates a small volume-like tree with media, sidecars,
// macOS junk, a package, and a symlink.
func buildTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "A001/A001C001.mov", randBytes(3*chunk+17, 1))
	write(t, root, "A001/A001C001.ale", []byte("Heading\nFIELD_DELIM\tTABS\n"))
	write(t, root, "AUDIO/SC01_T01.wav", randBytes(chunk+chunk/2, 2))
	write(t, root, "notes.txt", []byte("hello"))
	write(t, root, "scratch.tmp", []byte("tmp"))
	write(t, root, ".DS_Store", []byte("junk"))
	write(t, root, "A001/._A001C001.mov", []byte("resource fork"))
	write(t, root, ".Spotlight-V100/store.db", []byte("junk"))
	write(t, root, ".Trashes/501/old.mov", []byte("junk"))
	write(t, root, "Edit.fcpbundle/CurrentVersion.flexolibrary", randBytes(100, 3))
	write(t, root, "Edit.fcpbundle/sub/y", randBytes(50, 4))
	if err := os.Symlink(filepath.Join(root, "A001", "A001C001.mov"), filepath.Join(root, "link.mov")); err != nil {
		t.Fatal(err)
	}
	return root
}

func paths(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Path
	}
	return out
}

func find(t *testing.T, entries []Entry, path string) Entry {
	t.Helper()
	for _, e := range entries {
		if e.Path == path {
			return e
		}
	}
	t.Fatalf("entry %q not found in %v", path, paths(entries))
	return Entry{}
}

func TestWalkSkipsAndClassifies(t *testing.T) {
	root := buildTree(t)
	entries, err := Walk(context.Background(), root, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	got := paths(entries)
	for _, absent := range []string{
		".DS_Store", "A001/._A001C001.mov", ".Spotlight-V100", ".Spotlight-V100/store.db",
		".Trashes", ".Trashes/501/old.mov", "Edit.fcpbundle/sub", "Edit.fcpbundle/sub/y",
		"Edit.fcpbundle/CurrentVersion.flexolibrary",
	} {
		if slices.Contains(got, absent) {
			t.Errorf("%q should have been skipped; got %v", absent, got)
		}
	}

	mov := find(t, entries, "A001/A001C001.mov")
	if mov.Kind != KindVideo || mov.Ext != "mov" || mov.IsDir || mov.Size != 3*chunk+17 {
		t.Errorf("mov entry wrong: %+v", mov)
	}
	if mov.ModTime.IsZero() || mov.ModTime.Location() != time.UTC {
		t.Errorf("mov mtime not set in UTC: %v", mov.ModTime)
	}
	if mov.BirthTime == nil {
		t.Errorf("expected a birth time on macOS")
	}
	if e := find(t, entries, "A001/A001C001.ale"); e.Kind != KindSidecar {
		t.Errorf("ale kind = %s", e.Kind)
	}
	if e := find(t, entries, "AUDIO/SC01_T01.wav"); e.Kind != KindAudio {
		t.Errorf("wav kind = %s", e.Kind)
	}
	if e := find(t, entries, "A001"); !e.IsDir || e.Kind != KindDir || e.Size != 0 {
		t.Errorf("dir entry wrong: %+v", e)
	}
	pkg := find(t, entries, "Edit.fcpbundle")
	if !pkg.IsPackage || !pkg.IsDir || pkg.Kind != KindProject || pkg.Ext != "fcpbundle" || pkg.Size != 150 {
		t.Errorf("package entry wrong: %+v", pkg)
	}
	link := find(t, entries, "link.mov")
	if !link.IsSymlink || link.Size != 0 || link.Error != "" {
		t.Errorf("symlink entry wrong: %+v", link)
	}
	if slices.Index(got, "A001") > slices.Index(got, "AUDIO") || slices.Index(got, "A001/A001C001.ale") > slices.Index(got, "A001/A001C001.mov") {
		t.Errorf("entries not in lexical walk order: %v", got)
	}
}

func TestWalkDescendPackagesAndExtraSkip(t *testing.T) {
	root := buildTree(t)
	opts := DefaultOptions()
	opts.DescendPackages = true
	opts.Skip = append(opts.Skip, "*.tmp")
	entries, err := Walk(context.Background(), root, opts)
	if err != nil {
		t.Fatal(err)
	}
	got := paths(entries)
	if !slices.Contains(got, "Edit.fcpbundle/sub/y") {
		t.Errorf("expected to descend into package; got %v", got)
	}
	if e := find(t, entries, "Edit.fcpbundle"); e.IsPackage {
		t.Errorf("package flag should be off when descending: %+v", e)
	}
	if slices.Contains(got, "scratch.tmp") {
		t.Errorf("*.tmp should be skipped; got %v", got)
	}
}

func TestWalkRejectsFileRootAndHonoursCancel(t *testing.T) {
	root := buildTree(t)
	if _, err := Walk(context.Background(), filepath.Join(root, "notes.txt"), DefaultOptions()); err == nil {
		t.Error("expected error for non-directory root")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Walk(ctx, root, DefaultOptions()); err == nil {
		t.Error("expected error for canceled context")
	}
}

func expectedFingerprint(data []byte) string {
	h := xxhash.New()
	size := int64(len(data))
	if size > 2*chunk {
		h.Write(data[:chunk])
		h.Write(data[size-chunk:])
	} else {
		h.Write(data)
	}
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(size))
	h.Write(b[:])
	return fmt.Sprintf("%016x", h.Sum64())
}

func TestFingerprint(t *testing.T) {
	root := t.TempDir()
	cases := map[string]int{
		"empty":   0,
		"small":   5,
		"1mb":     chunk,
		"2mb":     2 * chunk,
		"2mb+1":   2*chunk + 1,
		"3mb+17":  3*chunk + 17,
		"10mb+99": 10*chunk + 99,
	}
	for name, n := range cases {
		data := randBytes(n, uint64(n)+7)
		p := write(t, root, name, data)
		got, err := Fingerprint(p, int64(n))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if want := expectedFingerprint(data); got != want {
			t.Errorf("%s: fingerprint %s, want %s", name, got, want)
		}
		full, err := FullHash(p)
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("%016x", xxhash.Sum64(data)); full != want {
			t.Errorf("%s: full hash %s, want %s", name, full, want)
		}
	}

	// A change in the middle of a large file is invisible to the
	// fingerprint but not to the full hash; a size change is visible to both.
	data := randBytes(3*chunk+17, 42)
	p := write(t, root, "big", data)
	fp1, _ := Fingerprint(p, int64(len(data)))
	fh1, _ := FullHash(p)
	data[len(data)/2] ^= 0xff
	write(t, root, "big", data)
	fp2, _ := Fingerprint(p, int64(len(data)))
	fh2, _ := FullHash(p)
	if fp1 != fp2 {
		t.Errorf("middle change altered fingerprint: %s vs %s", fp1, fp2)
	}
	if fh1 == fh2 {
		t.Errorf("middle change did not alter full hash")
	}
	data = append(data, 1)
	write(t, root, "big", data)
	fp3, _ := Fingerprint(p, int64(len(data)))
	if fp3 == fp1 {
		t.Errorf("size change did not alter fingerprint")
	}
}

func TestScanHashesOnlyFiles(t *testing.T) {
	root := buildTree(t)
	opts := DefaultOptions()
	opts.FullHash = true
	opts.Workers = 2
	var calls atomic.Int64
	var lastTotal atomic.Int64
	entries, err := Scan(context.Background(), root, opts, func(done, total int) {
		calls.Add(1)
		lastTotal.Store(int64(total))
	})
	if err != nil {
		t.Fatal(err)
	}
	var files int
	for _, e := range entries {
		hashed := e.Fingerprint != "" && e.FullHash != ""
		switch {
		case e.IsDir || e.IsSymlink || e.IsPackage:
			if e.Fingerprint != "" || e.FullHash != "" {
				t.Errorf("%s should not be hashed: %+v", e.Path, e)
			}
		default:
			files++
			if !hashed {
				t.Errorf("%s not hashed: %+v", e.Path, e)
			}
		}
	}
	if int(calls.Load()) != files || int(lastTotal.Load()) != files {
		t.Errorf("progress calls=%d total=%d, want %d", calls.Load(), lastTotal.Load(), files)
	}
	s := Summarize(entries)
	if s.Files != files || s.Dirs != 2 || s.Packages != 1 || s.Symlinks != 1 || s.Errors != 0 {
		t.Errorf("summary wrong: %+v", s)
	}
}

type snap struct {
	size  int64
	mtime time.Time
	mode  fs.FileMode
}

func snapshot(t *testing.T, root string) map[string]snap {
	t.Helper()
	m := map[string]snap{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		m[p] = snap{size: fi.Size(), mtime: fi.ModTime(), mode: fi.Mode()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestScanIsReadOnly(t *testing.T) {
	root := buildTree(t)
	before := snapshot(t, root)
	opts := DefaultOptions()
	opts.FullHash = true
	if _, err := Scan(context.Background(), root, opts, nil); err != nil {
		t.Fatal(err)
	}
	after := snapshot(t, root)
	if len(before) != len(after) {
		t.Fatalf("entry count changed: %d -> %d", len(before), len(after))
	}
	for p, b := range before {
		a, ok := after[p]
		if !ok {
			t.Errorf("%s disappeared", p)
			continue
		}
		if a != b {
			t.Errorf("%s changed: %+v -> %+v", p, b, a)
		}
	}
}

func TestKindOf(t *testing.T) {
	cases := map[string]Kind{
		"A001C001.MOV": KindVideo, "x.R3D": KindVideo, "x.braw": KindVideo, "frame.ari": KindVideo,
		"x.WAV": KindAudio, "x.ale": KindSidecar, "x.sidecar": KindSidecar, "x.RMD": KindSidecar,
		"x.dpx": KindImage, "x.drp": KindProject, "x.fcpbundle": KindProject,
		"README": KindOther, "x.unknownext": KindOther,
	}
	for name, want := range cases {
		if got := KindOf(name); got != want {
			t.Errorf("KindOf(%q) = %s, want %s", name, got, want)
		}
	}
}
