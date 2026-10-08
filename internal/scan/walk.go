package scan

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Walk lists every entry under root without reading file contents.
// Skipped names are omitted, package directories are recorded as single
// entries with their recursive size, and errors on individual entries are
// recorded in Entry.Error rather than aborting the walk. Entries are
// returned in lexical walk order.
func Walk(ctx context.Context, root string, opts Options) ([]Entry, error) {
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s: not a directory", root)
	}

	var entries []Entry
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == root {
			return walkErr
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		name := filepath.Base(p)

		if walkErr != nil {
			// WalkDir reports a directory twice when reading it fails:
			// once normally, then again with the error.
			if n := len(entries); n > 0 && entries[n-1].Path == rel {
				entries[n-1].Error = walkErr.Error()
			} else {
				entries = append(entries, Entry{
					Path: rel, Name: name, Kind: KindOther, Error: walkErr.Error(),
				})
			}
			return nil
		}

		if opts.skip(name) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		e := Entry{Path: rel, Name: name}

		if d.Type()&fs.ModeSymlink != 0 {
			e.IsSymlink = true
			e.Kind = KindOther
			e.Ext = extOf(name)
			if fi, err := os.Lstat(p); err == nil {
				e.ModTime = fi.ModTime().UTC()
				e.BirthTime = birthTime(fi)
			} else {
				e.Error = err.Error()
			}
			entries = append(entries, e)
			return nil
		}

		fi, err := d.Info()
		if err != nil {
			e.Kind = KindOther
			e.IsDir = d.IsDir()
			e.Error = err.Error()
			entries = append(entries, e)
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		e.ModTime = fi.ModTime().UTC()
		e.BirthTime = birthTime(fi)

		if d.IsDir() {
			e.IsDir = true
			e.Kind = KindDir
			if opts.isPackage(name) && !opts.DescendPackages {
				e.IsPackage = true
				e.Ext = extOf(name)
				if k := KindOf(name); k != KindOther {
					e.Kind = k
				}
				size, err := packageSize(ctx, p)
				if err != nil {
					e.Error = err.Error()
				}
				e.Size = size
				entries = append(entries, e)
				return fs.SkipDir
			}
			entries = append(entries, e)
			return nil
		}

		e.Size = fi.Size()
		e.Ext = extOf(name)
		e.Kind = KindOf(name)
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		return entries, err
	}
	return entries, nil
}

// packageSize sums the sizes of all regular files under dir.
func packageSize(ctx context.Context, dir string) (int64, error) {
	var total int64
	var firstErr error
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	if err != nil {
		return total, err
	}
	return total, firstErr
}

// Scan walks root and, if requested by opts, fingerprints every file.
// progress, if non-nil, is called from multiple goroutines during hashing.
func Scan(ctx context.Context, root string, opts Options, progress func(done, total int)) ([]Entry, error) {
	entries, err := Walk(ctx, root, opts)
	if err != nil {
		return entries, err
	}
	if opts.Fingerprint || opts.FullHash {
		if err := Hash(ctx, root, entries, opts, progress); err != nil {
			return entries, err
		}
	}
	return entries, nil
}

// unixZero reports whether t is the Unix epoch, which filesystems use for
// "unknown" birth times.
func unixZero(t time.Time) bool { return t.Unix() == 0 }
