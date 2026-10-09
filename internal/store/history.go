package store

import (
	"context"
	"fmt"
	"path/filepath"
	"time"
)

// Diff compares two scans by path. It is computed on the fly so any two
// scans can be compared, not just consecutive ones.
type Diff struct {
	A, B    Scan
	Added   []Change
	Removed []Change
	Changed []Change
	Total   int // files compared
}

// DiffScans lists files present only in b (added), only in a (removed), or
// different in size, mtime, or fingerprint (changed).
func (s *Store) DiffScans(ctx context.Context, aID, bID int64, limit int) (*Diff, error) {
	a, err := s.GetScan(ctx, aID)
	if err != nil {
		return nil, err
	}
	b, err := s.GetScan(ctx, bID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 1000
	}
	d := &Diff{A: a, B: b}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT coalesce(x.path, y.path), x.size, y.size,
		       CASE WHEN x.id IS NULL THEN 'added' WHEN y.id IS NULL THEN 'removed' ELSE 'changed' END
		FROM (SELECT id, path, size, mtime, fingerprint FROM entries WHERE scan_id = ? AND is_dir = 0 AND is_symlink = 0) x
		FULL OUTER JOIN (SELECT id, path, size, mtime, fingerprint FROM entries WHERE scan_id = ? AND is_dir = 0 AND is_symlink = 0) y
		  ON x.path = y.path
		WHERE x.id IS NULL OR y.id IS NULL OR x.size != y.size OR coalesce(x.mtime,'') != coalesce(y.mtime,'')
		   OR (x.fingerprint IS NOT NULL AND y.fingerprint IS NOT NULL AND x.fingerprint != y.fingerprint)
		ORDER BY 4, 1 LIMIT ?`, aID, bID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Change
		var oldSize, newSize *int64
		if err := rows.Scan(&c.Path, &oldSize, &newSize, &c.Change); err != nil {
			return nil, err
		}
		if oldSize != nil {
			c.OldSize = *oldSize
		}
		if newSize != nil {
			c.NewSize = *newSize
		}
		switch c.Change {
		case "added":
			d.Added = append(d.Added, c)
		case "removed":
			d.Removed = append(d.Removed, c)
		default:
			d.Changed = append(d.Changed, c)
		}
	}
	d.Total = a.FileCount + b.FileCount
	return d, rows.Err()
}

// Backup writes a consistent copy of the database to path using VACUUM INTO.
func (s *Store) Backup(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	// VACUUM INTO refuses to overwrite; the caller picks a fresh name.
	_, err = s.DB.ExecContext(ctx, `VACUUM INTO ?`, abs)
	return err
}

// BackupName returns a timestamped backup file name.
func BackupName(now time.Time) string {
	return fmt.Sprintf("shelf-%s.db", now.UTC().Format("20060102-150405"))
}
