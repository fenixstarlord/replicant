package scan

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/cespare/xxhash/v2"
)

// DefaultStreamLimit is the largest file a tape scan reads front to back
// instead of reading its head and tail. At LTO speeds (300-400 MB/s) one
// stop and reposition (~2 s plus the locate) costs about as much as
// streaming 1 GB, so smaller files are cheaper to read straight through.
const DefaultStreamLimit = 1 << 30

// tapeBuf is the read size for tape hashing. Large reads keep the drive
// streaming; io.Copy on an *os.File would use 32 KiB.
const tapeBuf = 4 << 20

// TapeHasher hashes files one at a time for a tape scan. Each file is read
// once, front to back where possible, and produces the same Fingerprint and
// FullHash values as Fingerprint and FullHash do on disk.
type TapeHasher struct {
	Fingerprint bool
	FullHash    bool
	// StreamLimit is the largest file read whole when only the fingerprint
	// is needed. Larger files are read at head and tail. Zero means
	// DefaultStreamLimit; negative means never stream.
	StreamLimit int64

	buf []byte
}

// TapeRead describes one file read by a TapeHasher.
type TapeRead struct {
	Bytes    int64 // bytes read from the file
	Streamed bool  // read front to back (false: head and tail only)
}

// Hash fills e.Fingerprint and/or e.FullHash from the file at path, or
// e.Error if it cannot be read. Directories, symlinks, packages and
// entries that already carry an error are left alone.
func (h *TapeHasher) Hash(ctx context.Context, path string, e *Entry) TapeRead {
	if !NeedsHash(e) || (!h.Fingerprint && !h.FullHash) {
		return TapeRead{}
	}
	if err := ctx.Err(); err != nil {
		e.Error = err.Error()
		return TapeRead{}
	}
	f, err := os.Open(path)
	if err != nil {
		e.Error = err.Error()
		return TapeRead{}
	}
	defer f.Close()
	return h.hashOpen(ctx, f, e)
}

// fileReader is what hashing needs from an open file.
type fileReader interface {
	io.Reader
	io.ReaderAt
}

func (h *TapeHasher) hashOpen(ctx context.Context, f fileReader, e *Entry) TapeRead {
	if h.buf == nil {
		h.buf = make([]byte, tapeBuf)
	}
	limit := h.StreamLimit
	if limit == 0 {
		limit = DefaultStreamLimit
	}
	if !h.FullHash && e.Size > 2*chunk && (limit < 0 || e.Size > limit) {
		fp, n, err := headTail(ctx, f, e.Size, h.buf)
		if err != nil {
			e.Error = err.Error()
		} else {
			e.Fingerprint = fp
		}
		return TapeRead{Bytes: n}
	}
	fp, fpErr, fh, n, err := streamHash(ctx, f, e.Size, h.buf)
	if err != nil {
		e.Error = err.Error()
		// Disk mode reads only the head and tail, so a bad block in the
		// middle would not stop it from fingerprinting the file. Keep the
		// error, which tells of damage on this copy, and still record the
		// fingerprint so the file matches its other copies.
		if h.Fingerprint && e.Size > 2*chunk && ctx.Err() == nil {
			if fp, _, err := headTail(ctx, f, e.Size, h.buf); err == nil {
				e.Fingerprint = fp
			}
		}
		return TapeRead{Bytes: n, Streamed: true}
	}
	if h.Fingerprint {
		if fpErr != nil {
			// As in disk mode, a fingerprint error also leaves FullHash unset.
			e.Error = fpErr.Error()
			return TapeRead{Bytes: n, Streamed: true}
		}
		e.Fingerprint = fp
	}
	if h.FullHash {
		e.FullHash = fh
	}
	return TapeRead{Bytes: n, Streamed: true}
}

// streamHash reads r to the end once and returns the Fingerprint for a file
// of the given size and the FullHash of everything read. For files over
// 2 MiB the fingerprint takes bytes [0, 1 MiB) and [size-1 MiB, size), which
// arrive in that order, so no buffering is needed. fpErr reports a file too
// short for its fingerprint, which does not affect the full hash; err
// reports a failed read.
func streamHash(ctx context.Context, r io.Reader, size int64, buf []byte) (fp string, fpErr error, full string, n int64, err error) {
	fpd, fhd := xxhash.New(), xxhash.New()
	small := size <= 2*chunk
	for {
		if err := ctx.Err(); err != nil {
			return "", nil, "", n, err
		}
		k, rerr := r.Read(buf)
		if k > 0 {
			b := buf[:k]
			fhd.Write(b)
			if small {
				fpd.Write(b)
			} else {
				fpd.Write(window(b, n, 0, chunk))
				fpd.Write(window(b, n, size-chunk, size))
			}
			n += int64(k)
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return "", nil, "", n, fmt.Errorf("read at %d: %w", n, rerr)
		}
	}
	full = fmt.Sprintf("%016x", fhd.Sum64())
	if !small && n < chunk {
		return "", fmt.Errorf("read head: %w", io.ErrUnexpectedEOF), full, n, nil
	}
	return sumWithSize(fpd, size), nil, full, n, nil
}

// window returns the part of b, which starts at offset off in the file,
// that falls inside [lo, hi).
func window(b []byte, off, lo, hi int64) []byte {
	start, end := max(lo-off, 0), min(hi-off, int64(len(b)))
	if start >= end {
		return nil
	}
	return b[start:end]
}

// headTail returns the Fingerprint of a file larger than 2 MiB from two
// reads: 1 MiB at the head and 1 MiB at size-1 MiB.
func headTail(ctx context.Context, f io.ReaderAt, size int64, buf []byte) (string, int64, error) {
	d := xxhash.New()
	if k, err := f.ReadAt(buf[:chunk], 0); k < chunk {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return "", int64(k), fmt.Errorf("read head: %w", err)
	}
	d.Write(buf[:chunk])
	if err := ctx.Err(); err != nil {
		return "", chunk, err
	}
	k, err := f.ReadAt(buf[:chunk], size-chunk)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", int64(chunk + k), fmt.Errorf("read tail: %w", err)
	}
	d.Write(buf[:k])
	return sumWithSize(d, size), int64(chunk + k), nil
}

// sumWithSize finishes a fingerprint by appending the size as a
// little-endian uint64, as Fingerprint does.
func sumWithSize(d *xxhash.Digest, size int64) string {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(size))
	d.Write(b[:])
	return fmt.Sprintf("%016x", d.Sum64())
}
