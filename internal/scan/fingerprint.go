package scan

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/cespare/xxhash/v2"
	"golang.org/x/sync/errgroup"
)

// chunk is the number of bytes hashed from each end of a file.
const chunk = 1 << 20

// Fingerprint returns a cheap content fingerprint for a file of the given
// size: xxHash64 over the first 1 MiB, the last 1 MiB, and the size as a
// little-endian uint64. Files of 2 MiB or less are hashed in full (plus
// size), so head and tail never overlap. The result is 16 hex characters.
func Fingerprint(path string, size int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := xxhash.New()
	if size > 2*chunk {
		if _, err := io.CopyN(h, f, chunk); err != nil {
			return "", fmt.Errorf("read head: %w", err)
		}
		if _, err := f.Seek(size-chunk, io.SeekStart); err != nil {
			return "", fmt.Errorf("seek tail: %w", err)
		}
		if _, err := io.CopyN(h, f, chunk); err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read tail: %w", err)
		}
	} else {
		if _, err := io.Copy(h, f); err != nil {
			return "", err
		}
	}
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(size))
	h.Write(b[:])
	return fmt.Sprintf("%016x", h.Sum64()), nil
}

// FullHash returns xxHash64 over the entire file as 16 hex characters.
func FullHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := xxhash.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%016x", h.Sum64()), nil
}

// Hash fills Fingerprint and/or FullHash on every regular-file entry,
// using a bounded worker pool. A file that cannot be read gets its error
// recorded in Entry.Error; only context cancellation aborts the whole run.
// progress, if non-nil, is called from worker goroutines and must be safe
// for concurrent use.
func Hash(ctx context.Context, root string, entries []Entry, opts Options, progress func(done, total int)) error {
	if !opts.Fingerprint && !opts.FullHash {
		return nil
	}
	var total int
	for i := range entries {
		if needsHash(&entries[i]) {
			total++
		}
	}
	var done atomic.Int64
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(opts.workers())
	for i := range entries {
		e := &entries[i]
		if !needsHash(e) {
			continue
		}
		g.Go(func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			p := filepath.Join(root, filepath.FromSlash(e.Path))
			if opts.Fingerprint {
				fp, err := Fingerprint(p, e.Size)
				if err != nil {
					e.Error = err.Error()
				} else {
					e.Fingerprint = fp
				}
			}
			if opts.FullHash && e.Error == "" {
				fh, err := FullHash(p)
				if err != nil {
					e.Error = err.Error()
				} else {
					e.FullHash = fh
				}
			}
			if progress != nil {
				progress(int(done.Add(1)), total)
			}
			return nil
		})
	}
	return g.Wait()
}

func needsHash(e *Entry) bool {
	return !e.IsDir && !e.IsSymlink && !e.IsPackage && e.Error == ""
}
