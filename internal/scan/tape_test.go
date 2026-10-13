package scan

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// tapeSizes straddle every boundary in Fingerprint: the 1 MiB head, the
// 2 MiB whole-file cutoff, and multi-chunk files.
var tapeSizes = []int{0, 1, 1000, chunk - 1, chunk, chunk + 1, 2*chunk - 1, 2 * chunk, 2*chunk + 1, 3*chunk + 123, 17*chunk + 5}

func TestTapeHasherMatchesDiskHashes(t *testing.T) {
	root := t.TempDir()
	for _, n := range tapeSizes {
		p := write(t, root, "f", randBytes(n, uint64(n)+7))
		wantFP, err := Fingerprint(p, int64(n))
		if err != nil {
			t.Fatal(err)
		}
		wantFH, err := FullHash(p)
		if err != nil {
			t.Fatal(err)
		}
		cases := []struct {
			name         string
			h            TapeHasher
			wantStreamed bool
		}{
			{"stream", TapeHasher{Fingerprint: true}, true},
			{"head-tail", TapeHasher{Fingerprint: true, StreamLimit: -1}, n <= 2*chunk},
			{"limit-below-size", TapeHasher{Fingerprint: true, StreamLimit: int64(n) - 1}, n <= 2*chunk},
			{"full-hash", TapeHasher{Fingerprint: true, FullHash: true, StreamLimit: -1}, true},
			{"full-hash-only", TapeHasher{FullHash: true}, true},
		}
		for _, tc := range cases {
			e := Entry{Path: "f", Size: int64(n)}
			r := tc.h.Hash(context.Background(), p, &e)
			if e.Error != "" {
				t.Fatalf("size %d %s: %s", n, tc.name, e.Error)
			}
			if tc.h.Fingerprint && e.Fingerprint != wantFP {
				t.Errorf("size %d %s: fingerprint %s, want %s", n, tc.name, e.Fingerprint, wantFP)
			}
			if !tc.h.Fingerprint && e.Fingerprint != "" {
				t.Errorf("size %d %s: fingerprint set but not requested", n, tc.name)
			}
			if tc.h.FullHash && e.FullHash != wantFH {
				t.Errorf("size %d %s: full hash %s, want %s", n, tc.name, e.FullHash, wantFH)
			}
			if r.Streamed != tc.wantStreamed {
				t.Errorf("size %d %s: streamed=%v, want %v", n, tc.name, r.Streamed, tc.wantStreamed)
			}
			wantBytes := int64(n)
			if !r.Streamed {
				wantBytes = 2 * chunk
			}
			if r.Bytes != wantBytes {
				t.Errorf("size %d %s: read %d bytes, want %d", n, tc.name, r.Bytes, wantBytes)
			}
		}
	}
}

func TestStreamHashAnyReadSize(t *testing.T) {
	root := t.TempDir()
	for _, n := range []int{2*chunk - 7, 3*chunk + 4321} {
		p := write(t, root, "f", randBytes(n, uint64(n)))
		want, _ := Fingerprint(p, int64(n))
		for _, bufSize := range []int{1000, 4096, chunk - 3, chunk, chunk + 5, 5 * chunk} {
			f, err := os.Open(p)
			if err != nil {
				t.Fatal(err)
			}
			got, fpErr, _, read, err := streamHash(context.Background(), f, int64(n), make([]byte, bufSize))
			f.Close()
			if err != nil || fpErr != nil || got != want || read != int64(n) {
				t.Errorf("size %d buf %d: %s read %d %v %v, want %s", n, bufSize, got, read, err, fpErr, want)
			}
		}
	}
}

// A size that no longer matches the file must give what disk-mode Hash
// gives: the same values, and an error in both or in neither.
func TestTapeHasherSizeMismatchMatchesDisk(t *testing.T) {
	root := t.TempDir()
	for _, actual := range []int{5, chunk - 1, 3*chunk + 10} {
		write(t, root, "f", randBytes(actual, uint64(actual)))
		for _, recorded := range []int64{int64(actual) - 5, int64(actual) + 5, int64(actual) + 2*chunk, 4 * chunk, 2*chunk + 1} {
			if recorded < 0 {
				continue
			}
			for _, flags := range []Options{{Fingerprint: true}, {FullHash: true}, {Fingerprint: true, FullHash: true}} {
				disk := []Entry{{Path: "f", Size: recorded}}
				if err := Hash(context.Background(), root, disk, flags, nil); err != nil {
					t.Fatal(err)
				}
				for _, limit := range []int64{0, -1} {
					e := Entry{Path: "f", Size: recorded}
					h := TapeHasher{Fingerprint: flags.Fingerprint, FullHash: flags.FullHash, StreamLimit: limit}
					h.Hash(context.Background(), filepath.Join(root, "f"), &e)
					if (disk[0].Error != "") != (e.Error != "") || e.Fingerprint != disk[0].Fingerprint || e.FullHash != disk[0].FullHash {
						t.Errorf("actual %d recorded %d %+v limit %d: tape %q/%q err %q, disk %q/%q err %q", actual, recorded, flags, limit,
							e.Fingerprint, e.FullHash, e.Error, disk[0].Fingerprint, disk[0].FullHash, disk[0].Error)
					}
				}
			}
		}
	}
}

// chaosReader returns data in random short pieces, sometimes together
// with io.EOF, as a FUSE filesystem may.
type chaosReader struct {
	data []byte
	r    *rand.Rand
}

func (c *chaosReader) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := min(1+c.r.IntN(len(p)), len(c.data))
	if c.r.IntN(4) == 0 {
		n = min(1+c.r.IntN(13), n)
	}
	copy(p, c.data[:n])
	c.data = c.data[n:]
	if len(c.data) == 0 && c.r.IntN(2) == 0 {
		return n, io.EOF
	}
	return n, nil
}

func TestStreamHashShortReads(t *testing.T) {
	root := t.TempDir()
	for i, n := range []int{0, 1, chunk - 1, chunk + 1, 2 * chunk, 2*chunk + 1, 3*chunk + 777} {
		data := randBytes(n, uint64(i)+40)
		p := write(t, root, "f", data)
		wantFP, _ := Fingerprint(p, int64(n))
		wantFH, _ := FullHash(p)
		for seed := range uint64(5) {
			r := &chaosReader{data: data, r: rand.New(rand.NewPCG(seed, uint64(n)))}
			fp, fpErr, fh, read, err := streamHash(context.Background(), r, int64(n), make([]byte, 64<<10))
			if err != nil || fpErr != nil || fp != wantFP || fh != wantFH || read != int64(n) {
				t.Errorf("size %d seed %d: %s %s read %d %v %v", n, seed, fp, fh, read, err, fpErr)
			}
		}
	}
}

// failingFile serves data but fails reads that touch [bad, bad+1).
type failingFile struct {
	data []byte
	bad  int64
	off  int64
}

func (f *failingFile) Read(p []byte) (int, error) {
	if f.off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n, err := f.ReadAt(p, f.off)
	f.off += int64(n)
	return n, err
}

func (f *failingFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	end := min(off+int64(len(p)), int64(len(f.data)))
	if off <= f.bad && f.bad < end {
		n := copy(p, f.data[off:f.bad])
		return n, unix.EIO
	}
	n := copy(p, f.data[off:end])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func TestTapeHasherBadBlockKeepsFingerprint(t *testing.T) {
	n := 8 * chunk
	data := randBytes(n, 77)
	p := write(t, t.TempDir(), "f", data)
	want, _ := Fingerprint(p, int64(n))
	cases := []struct {
		name    string
		bad     int64
		h       TapeHasher
		wantFP  bool
		wantErr bool
	}{
		{"bad block mid-file, fingerprint only", 4 * chunk, TapeHasher{Fingerprint: true}, true, true},
		{"bad block mid-file, with full hash", 4 * chunk, TapeHasher{Fingerprint: true, FullHash: true}, true, true},
		{"bad block in the head", 10, TapeHasher{Fingerprint: true}, false, true},
		{"bad block in the tail", int64(n) - 10, TapeHasher{Fingerprint: true}, false, true},
		{"bad block mid-file, head and tail only", 4 * chunk, TapeHasher{Fingerprint: true, StreamLimit: -1}, true, false},
	}
	for _, tc := range cases {
		e := Entry{Path: "f", Size: int64(n)}
		tc.h.hashOpen(context.Background(), &failingFile{data: data, bad: tc.bad}, &e)
		if (e.Fingerprint == want) != tc.wantFP || (e.Fingerprint != "" && e.Fingerprint != want) || (e.Error != "") != tc.wantErr || e.FullHash != "" {
			t.Errorf("%s: fingerprint %q (want %v) error %q full %q", tc.name, e.Fingerprint, tc.wantFP, e.Error, e.FullHash)
		}
	}
}

func TestTapeHasherSkipsAndCancels(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "f", randBytes(3*chunk, 4))
	h := TapeHasher{Fingerprint: true}
	for _, e := range []Entry{{IsDir: true}, {IsSymlink: true}, {IsPackage: true}, {Error: "walk failed"}} {
		if r := h.Hash(context.Background(), p, &e); r.Bytes != 0 || e.Fingerprint != "" {
			t.Errorf("%+v should be skipped", e)
		}
	}
	if r := (&TapeHasher{}).Hash(context.Background(), p, &Entry{Size: 3 * chunk}); r.Bytes != 0 {
		t.Errorf("nothing requested, but read %d bytes", r.Bytes)
	}
	missing := Entry{Size: 10}
	h.Hash(context.Background(), filepath.Join(root, "nope"), &missing)
	if missing.Error == "" {
		t.Error("missing file should record an error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, limit := range []int64{0, -1} {
		e := Entry{Size: 3 * chunk}
		r := (&TapeHasher{Fingerprint: true, StreamLimit: limit}).Hash(ctx, p, &e)
		if e.Error == "" || e.Fingerprint != "" || r.Bytes != 0 {
			t.Errorf("limit %d: cancelled hash should record an error and read nothing: %+v %+v", limit, e, r)
		}
	}
}

// setLTFSAttr fakes an LTFS virtual attribute, or skips the test where the
// filesystem does not take user extended attributes.
func setLTFSAttr(t *testing.T, path, name, value string) {
	t.Helper()
	err := unix.Setxattr(path, ltfsAttrPrefix+"ltfs."+name, []byte(value), 0)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EPERM) {
		t.Skipf("extended attributes not supported here: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadTapePositionAndDetectLTFS(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, "clip.mxf", []byte("data"))
	if _, ok := ReadTapePosition(p); ok {
		t.Fatal("a plain file has no tape position")
	}
	if _, ok := DetectLTFS(root); ok {
		t.Fatal("a plain directory is not LTFS")
	}
	setLTFSAttr(t, p, "partition", "b")
	setLTFSAttr(t, p, "startblock", "123456")
	if pos, ok := ReadTapePosition(p); !ok || pos != (TapePosition{Partition: "b", Block: 123456}) {
		t.Errorf("position = %+v %v", pos, ok)
	}
	bad := write(t, root, "bad.mxf", []byte("data"))
	setLTFSAttr(t, bad, "partition", "b")
	setLTFSAttr(t, bad, "startblock", "not a number")
	if _, ok := ReadTapePosition(bad); ok {
		t.Error("an unparsable start block is no position")
	}

	long := strings.Repeat("x", 300) // longer than ltfsAttr's first buffer
	setLTFSAttr(t, bad, "volumeName", long)
	if got, err := ltfsAttr(bad, "volumeName"); err != nil || got != long {
		t.Errorf("long attribute: %d bytes, %v", len(got), err)
	}

	setLTFSAttr(t, root, "volumeUUID", "8d6a3a4e-0b1c-4f55-9a77-3c2a1d0e9f10")
	setLTFSAttr(t, root, "volumeName", "TAPE001")
	tape, ok := DetectLTFS(root)
	if !ok || tape.UUID != "8d6a3a4e-0b1c-4f55-9a77-3c2a1d0e9f10" || tape.Name != "TAPE001" {
		t.Fatalf("DetectLTFS = %+v %v", tape, ok)
	}
	v, err := VolumeInfo(root)
	if err != nil {
		t.Fatal(err)
	}
	if !v.LTFS || v.UUID != tape.UUID || v.FSType != "ltfs" || v.Name != "TAPE001" {
		t.Errorf("VolumeInfo on LTFS = %+v", v)
	}
}

func TestTapePositionLess(t *testing.T) {
	a1, a9, b0 := TapePosition{"a", 1}, TapePosition{"a", 9}, TapePosition{"b", 0}
	if !a1.Less(a9) || a9.Less(a1) || !a9.Less(b0) || b0.Less(a1) || a1.Less(a1) {
		t.Error("Less must order by partition, then block")
	}
}
