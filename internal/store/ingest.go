package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fenixstarlord/replicant/internal/bundle"
	"github.com/fenixstarlord/replicant/internal/meta"
	"github.com/fenixstarlord/replicant/internal/scan"
)

// IngestResult summarises a stored scan.
type IngestResult struct {
	ScanID    int64  `json:"scan_id"`
	DriveID   int64  `json:"drive_id"`
	DriveName string `json:"drive_name"`
	Entries   int    `json:"entries"`
	Files     int    `json:"files"`
	Clips     int    `json:"clips"`
	Added     int    `json:"added"`
	Removed   int    `json:"removed"`
	Changed   int    `json:"changed"`
	IsLatest  bool   `json:"is_latest"`
	IsPartial bool   `json:"is_partial"`
	Root      string `json:"root"`
	FirstScan bool   `json:"first_scan"`
}

// driveKey identifies a drive; see scan.Volume.Key.
func driveKey(v scan.Volume) string { return v.Key() }

// upsertDrive records the drive a bundle came from and returns its id and
// catalog name. chosen is a name the user gave at scan time ("" for none;
// the volume's own name means "follow the volume" again). Only a bundle at
// least as new as the drive's newest scan may change what the catalog
// records about the volume, so a scan ingested late (an old -o file)
// cannot undo a newer rename or bring back old capacity figures.
func upsertDrive(ctx context.Context, tx *sql.Tx, vol scan.Volume, volName, chosen string, scannedAt time.Time, now string) (int64, string, error) {
	key := driveKey(vol)
	var (
		id         int64
		curName    string
		curRenamed bool
	)
	err := tx.QueryRowContext(ctx, `SELECT id, name, renamed FROM drives WHERE volume_uuid = ?`, key).Scan(&id, &curName, &curRenamed)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		id = 0
	case err != nil:
		return 0, "", err
	}
	if id != 0 {
		newer, err := hasNewerScan(ctx, tx, id, scannedAt)
		if err != nil {
			return 0, "", err
		}
		if newer {
			_, err := tx.ExecContext(ctx, `UPDATE drives SET last_seen = ? WHERE id = ?`, now, id)
			return id, curName, err
		}
	}
	// A pre-scan profile does not create a catalog drive. Apply its current
	// name/taxonomy only on first ingest, atomically with the scan. Later ingests
	// keep web/profile edits, even if an older client sends a stale scan name.
	newDrive := id == 0
	var profileName string
	var profileGroup, profileClient sql.NullInt64
	perr := tx.QueryRowContext(ctx, `SELECT name,group_id,client_id FROM drive_profiles WHERE volume_key=?`, key).Scan(&profileName, &profileGroup, &profileClient)
	if perr != nil && !errors.Is(perr, sql.ErrNoRows) {
		return 0, "", perr
	}
	if perr == nil {
		chosen = ""
		if newDrive {
			chosen = profileName
		}
	}

	name, renamed := volName, false
	switch {
	case chosen != "" && chosen != volName:
		name, renamed = chosen, true
	case chosen == "" && id != 0 && curRenamed:
		name, renamed = curName, true
	}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO drives(volume_uuid, name, volume_name, renamed, fs_type, capacity_bytes, free_bytes, device, media_name, protocol, first_seen, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(volume_uuid) DO UPDATE SET
			name = excluded.name, renamed = excluded.renamed, volume_name = excluded.volume_name,
			fs_type = excluded.fs_type,
			capacity_bytes = excluded.capacity_bytes, free_bytes = excluded.free_bytes,
			device = excluded.device, media_name = excluded.media_name, protocol = excluded.protocol,
			last_seen = excluded.last_seen
		RETURNING id`,
		key, name, volName, renamed, vol.FSType, vol.TotalBytes, vol.FreeBytes, vol.Device, vol.MediaName, vol.Protocol, now, now,
	).Scan(&id)
	if err == nil && newDrive && perr == nil {
		_, err = tx.ExecContext(ctx, `UPDATE drives SET group_id=?,client_id=? WHERE id=?`, profileGroup, profileClient, id)
	}
	return id, name, err
}

// hasNewerScan reports whether the drive has a scan taken after t. Times
// are compared parsed: stored RFC 3339 strings with trimmed fractions do
// not sort as text.
func hasNewerScan(ctx context.Context, tx *sql.Tx, driveID int64, t time.Time) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT scanned_at FROM scans WHERE drive_id = ?`, driveID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return false, err
		}
		if at, err := time.Parse(time.RFC3339Nano, v); err == nil && at.After(t) {
			return true, nil
		}
	}
	return false, rows.Err()
}

type fileSig struct {
	size        int64
	mtime       string
	fingerprint string
}

// Ingest stores a bundle as a new scan in one transaction. On any error
// nothing is written.
func (s *Store) Ingest(ctx context.Context, b *bundle.Bundle) (res IngestResult, err error) {
	return s.IngestFrom(ctx, b, "", 0)
}

// IngestFrom is Ingest with a record of where the bundle came from (the
// API key name, "server" for the server's own scans, "file" for the ingest
// command, or "local" for the standalone app) and the activity that
// produced it, if the scanner reported one; otherwise a finished activity
// row is written so every scan shows in the history.
func (s *Store) IngestFrom(ctx context.Context, b *bundle.Bundle, source string, activityID int64) (res IngestResult, err error) {
	res, err = s.ingest(ctx, b, source)
	if err != nil {
		return res, err
	}
	if activityID > 0 {
		// Show the drive under its catalog name, which may differ from the
		// name the scanner reported when it started.
		if _, uerr := s.DB.ExecContext(ctx, `UPDATE activity SET drive_name = ? WHERE id = ? AND status = 'running'`,
			res.DriveName, activityID); uerr != nil {
			return res, fmt.Errorf("activity name: %w", uerr)
		}
		if ferr := s.FinishActivity(ctx, activityID, "done", res.ScanID, ""); ferr != nil {
			return res, fmt.Errorf("finish activity: %w", ferr)
		}
		return res, nil
	}
	m := b.Manifest
	now := time.Now().UTC().Format(time.RFC3339)
	_, aerr := s.DB.ExecContext(ctx, `INSERT INTO activity(host, source, drive_name, volume_uuid, root, stage, status, scan_id, started_at, updated_at, finished_at)
		VALUES (?, ?, ?, ?, ?, 'ingest', 'done', ?, ?, ?, ?)`,
		m.Host, source, res.DriveName, m.Volume.UUID, m.Root, res.ScanID, m.ScannedAt.UTC().Format(time.RFC3339Nano), now, now)
	if aerr != nil {
		return res, fmt.Errorf("record activity: %w", aerr)
	}
	return res, nil
}

func (s *Store) ingest(ctx context.Context, b *bundle.Bundle, source string) (res IngestResult, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	m := b.Manifest
	now := time.Now().UTC().Format(time.RFC3339)
	vol := m.Volume
	volName := vol.Name
	if volName == "" {
		volName = vol.MountPoint
	}
	driveID, name, err := upsertDrive(ctx, tx, vol, volName, strings.TrimSpace(m.DriveName), m.ScannedAt, now)
	if err != nil {
		return res, fmt.Errorf("drive upsert: %w", err)
	}

	// A scan of a subfolder is partial: latest and diffs are per root.
	root := m.Root
	partial := vol.MountPoint != "" && root != "" && strings.TrimRight(root, "/") != strings.TrimRight(vol.MountPoint, "/")

	// Previous latest scan of the same root, for the diff.
	var prevID sql.NullInt64
	var prevScannedAt sql.NullString
	if err = tx.QueryRowContext(ctx,
		`SELECT id, scanned_at FROM scans WHERE drive_id = ? AND root = ? AND is_latest = 1`, driveID, root,
	).Scan(&prevID, &prevScannedAt); err != nil && err != sql.ErrNoRows {
		return res, err
	}
	err = nil

	optsJSON, _ := json.Marshal(m.Options)
	extJSON, _ := json.Marshal(m.Extractors)
	if m.Extractors == nil {
		extJSON = []byte("[]")
	}
	var scanID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO scans(drive_id, scanned_at, ingested_at, scanner_version, root, is_partial, options_json, extractors_json, host, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		driveID, m.ScannedAt.UTC().Format(time.RFC3339Nano), now, m.ScannerVersion, root, boolInt(partial), string(optsJSON), string(extJSON), m.Host, source,
	).Scan(&scanID)
	if err != nil {
		return res, fmt.Errorf("scan insert: %w", err)
	}

	// Per-directory recursive totals, computed once so browsing is instant.
	type agg struct {
		size  int64
		files int
	}
	dirAgg := map[string]*agg{}
	var files, dirs int
	var totalBytes int64
	for _, e := range b.Entries {
		if e.IsDir && !e.IsPackage {
			dirs++
			if dirAgg[e.Path] == nil {
				dirAgg[e.Path] = &agg{}
			}
			continue
		}
		if e.IsSymlink {
			continue
		}
		files++
		totalBytes += e.Size
		for d := e.Dir(); ; d = parentOf(d) {
			a := dirAgg[d]
			if a == nil {
				a = &agg{}
				dirAgg[d] = a
			}
			a.size += e.Size
			a.files++
			if d == "" {
				break
			}
		}
	}

	entryIns := newBatchInserter(tx, `INSERT INTO entries(scan_id, parent_path, path, name, ext, kind, is_dir, is_package, is_symlink,
		size, mtime, btime, fingerprint, full_hash, clip_key, error, dir_total_size, dir_file_count) VALUES `, 18)
	for i := range b.Entries {
		e := &b.Entries[i]
		var dirSize, dirFiles any
		if e.IsDir && !e.IsPackage {
			if a := dirAgg[e.Path]; a != nil {
				dirSize, dirFiles = a.size, a.files
			} else {
				dirSize, dirFiles = 0, 0
			}
		}
		if err = entryIns.add(ctx,
			scanID, e.Dir(), e.Path, e.Name, e.Ext, string(e.Kind), boolInt(e.IsDir), boolInt(e.IsPackage), boolInt(e.IsSymlink),
			e.Size, fmtTime(e.ModTime), fmtTimePtr(e.BirthTime), nullStr(e.Fingerprint), nullStr(e.FullHash),
			nullStr(e.ClipID), nullStr(e.Error), dirSize, dirFiles,
		); err != nil {
			return res, fmt.Errorf("entry %q: %w", e.Path, err)
		}
	}
	if err = entryIns.flush(ctx); err != nil {
		return res, fmt.Errorf("entries: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO entries_fts(rowid, name)
		SELECT id, name FROM entries WHERE scan_id = ?`, scanID); err != nil {
		return res, fmt.Errorf("entries fts: %w", err)
	}

	clipIns := newBatchInserter(tx, `INSERT INTO clips(scan_id, clip_key, root_path, name, kind, file_count, total_size, mtime,
		files_json, sidecars_json, seq_frame_count, seq_first_frame, seq_last_frame,
		clip_name, reel, camera_index, scene, take, circled, recorded_at, tc_start, tc_end, duration_s, frame_count,
		container, codec, codec_detail, width, height, sensor_mode, fps, capture_fps, bit_depth, color_gamma, squeeze,
		camera_make, camera_model, camera_serial, firmware, iso, wb_kelvin, tint, shutter_angle, shutter_speed, nd,
		lens, focal_mm, t_stop, focus_distance, audio_channels, sample_rate, audio_bit_depth, look,
		field_sources_json, errors_json) VALUES `, 55)
	for i := range b.Clips {
		c := &b.Clips[i]
		filesJSON, _ := json.Marshal(c.Files)
		if c.Files == nil {
			filesJSON = []byte("[]")
		}
		sidecarsJSON, _ := json.Marshal(c.Sidecars)
		if c.Sidecars == nil {
			sidecarsJSON = []byte("[]")
		}
		var fc, ff, lf any
		if c.FrameCount > 0 {
			fc, ff, lf = c.FrameCount, c.FirstFrame, c.LastFrame
		}
		m := c.Meta
		if m == nil {
			m = &meta.Fields{}
		}
		sourcesJSON, _ := json.Marshal(c.Sources)
		if c.Sources == nil {
			sourcesJSON = []byte("{}")
		}
		errorsJSON, _ := json.Marshal(c.Errors)
		if c.Errors == nil {
			errorsJSON = []byte("[]")
		}
		if err = clipIns.add(ctx,
			scanID, c.ID, c.RootPath, c.Name, string(c.Kind), c.FileCount, c.TotalSize, fmtTime(c.ModTime),
			string(filesJSON), string(sidecarsJSON), fc, ff, lf,
			ptrStr(m.ClipName), ptrStr(m.Reel), ptrStr(m.CameraIndex), ptrStr(m.Scene), ptrStr(m.Take), ptrBool(m.Circled),
			fmtTimePtr(m.RecordedAt), ptrStr(m.TCStart), ptrStr(m.TCEnd), ptrFloat(m.DurationS), ptrInt(m.FrameCount),
			ptrStr(m.Container), ptrStr(m.Codec), ptrStr(m.CodecDetail), ptrInt(m.Width), ptrInt(m.Height), ptrStr(m.SensorMode),
			ptrFloat(m.FPS), ptrFloat(m.CaptureFPS), ptrInt(m.BitDepth), ptrStr(m.ColorGamma), ptrFloat(m.Squeeze),
			ptrStr(m.CameraMake), ptrStr(m.CameraModel), ptrStr(m.CameraSerial), ptrStr(m.Firmware),
			ptrInt(m.ISO), ptrInt(m.WBKelvin), ptrFloat(m.Tint), ptrFloat(m.ShutterAngle), ptrStr(m.ShutterSpeed), ptrStr(m.ND),
			ptrStr(m.Lens), ptrFloat(m.FocalMM), ptrFloat(m.TStop), ptrStr(m.FocusDistance),
			ptrInt(m.AudioChannels), ptrInt(m.SampleRate), ptrInt(m.AudioBitDepth), ptrStr(m.Look),
			string(sourcesJSON), string(errorsJSON),
		); err != nil {
			return res, fmt.Errorf("clip %q: %w", c.RootPath, err)
		}
	}
	if err = clipIns.flush(ctx); err != nil {
		return res, fmt.Errorf("clips: %w", err)
	}

	// Verbatim extractor output per clip. Keyed by the bundle's clip key
	// since clip ids are assigned by SQLite.
	for i := range b.Clips {
		c := &b.Clips[i]
		for source, raw := range c.Raw {
			if len(raw) == 0 {
				continue
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO clip_raw(clip_id, source, raw_json)
				SELECT id, ?, ? FROM clips WHERE scan_id = ? AND clip_key = ?`, source, string(raw), scanID, c.ID); err != nil {
				return res, fmt.Errorf("clip_raw %q/%s: %w", c.RootPath, source, err)
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO clips_fts(clip_id, scan_id, name, reel, scene, take, camera, lens, look, path)
		SELECT id, scan_id, name, coalesce(reel,''), coalesce(scene,''), coalesce(take,''),
			coalesce(camera_model,''), coalesce(lens,''), coalesce(look,''), root_path
		FROM clips WHERE scan_id = ?`, scanID); err != nil {
		return res, fmt.Errorf("clips fts: %w", err)
	}

	// Diff against the previous latest scan, by path.
	var added, removed, changed int
	if prevID.Valid {
		prev := map[string]fileSig{}
		rows, qerr := tx.QueryContext(ctx,
			`SELECT path, size, coalesce(mtime,''), coalesce(fingerprint,'') FROM entries
			 WHERE scan_id = ? AND is_dir = 0 AND is_symlink = 0`, prevID.Int64)
		if qerr != nil {
			err = qerr
			return res, err
		}
		for rows.Next() {
			var p string
			var sig fileSig
			if err = rows.Scan(&p, &sig.size, &sig.mtime, &sig.fingerprint); err != nil {
				rows.Close()
				return res, err
			}
			prev[p] = sig
		}
		rows.Close()

		chg := newBatchInserter(tx, `INSERT INTO scan_changes(scan_id, path, change, old_size, new_size) VALUES `, 5)
		for _, e := range b.Entries {
			if e.IsDir || e.IsSymlink {
				continue
			}
			cur := fileSig{size: e.Size, mtime: fmtTimeStr(e.ModTime), fingerprint: e.Fingerprint}
			old, ok := prev[e.Path]
			switch {
			case !ok:
				added++
				err = chg.add(ctx, scanID, e.Path, "added", nil, e.Size)
			case old.size != cur.size || old.mtime != cur.mtime ||
				(old.fingerprint != "" && cur.fingerprint != "" && old.fingerprint != cur.fingerprint):
				changed++
				err = chg.add(ctx, scanID, e.Path, "changed", old.size, e.Size)
			}
			if err != nil {
				return res, err
			}
			delete(prev, e.Path)
		}
		for p, old := range prev {
			removed++
			if err = chg.add(ctx, scanID, p, "removed", old.size, nil); err != nil {
				return res, err
			}
		}
		if err = chg.flush(ctx); err != nil {
			return res, err
		}
	} else {
		added = files
	}

	// Latest = the scan with the newest scanned_at for this drive.
	isLatest := !prevID.Valid || m.ScannedAt.UTC().Format(time.RFC3339Nano) >= prevScannedAt.String
	if _, err = tx.ExecContext(ctx, `
		UPDATE scans SET file_count = ?, dir_count = ?, clip_count = ?, total_bytes = ?, added = ?, removed = ?, changed = ?
		WHERE id = ?`, files, dirs, len(b.Clips), totalBytes, added, removed, changed, scanID); err != nil {
		return res, err
	}
	if isLatest {
		if _, err = tx.ExecContext(ctx, `UPDATE scans SET is_latest = CASE WHEN id = ? THEN 1 ELSE 0 END WHERE drive_id = ? AND root = ?`,
			scanID, driveID, root); err != nil {
			return res, err
		}
	}
	if err = tx.Commit(); err != nil {
		return res, err
	}
	return IngestResult{
		ScanID: scanID, DriveID: driveID, DriveName: name,
		Entries: len(b.Entries), Files: files, Clips: len(b.Clips),
		Added: added, Removed: removed, Changed: changed,
		IsLatest: isLatest, IsPartial: partial, Root: root, FirstScan: !prevID.Valid,
	}, nil
}

func parentOf(dir string) string {
	if i := strings.LastIndexByte(dir, '/'); i >= 0 {
		return dir[:i]
	}
	return ""
}

func fmtTimeStr(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// batchInserter groups rows into multi-row INSERT statements, which is
// several times faster than one statement per row on the pure-Go driver.
type batchInserter struct {
	tx      *sql.Tx
	prefix  string
	cols    int
	perStmt int
	args    []any
	rows    int
	tuple   string
}

func newBatchInserter(tx *sql.Tx, prefix string, cols int) *batchInserter {
	// Stay well under SQLite's default 32766 bound-parameter limit.
	per := 2000 / cols
	if per < 1 {
		per = 1
	}
	return &batchInserter{
		tx: tx, prefix: prefix, cols: cols, perStmt: per,
		tuple: "(" + strings.TrimSuffix(strings.Repeat("?,", cols), ",") + ")",
	}
}

func (b *batchInserter) add(ctx context.Context, args ...any) error {
	if len(args) != b.cols {
		return fmt.Errorf("batch insert: got %d args, want %d", len(args), b.cols)
	}
	b.args = append(b.args, args...)
	b.rows++
	if b.rows >= b.perStmt {
		return b.flush(ctx)
	}
	return nil
}

func (b *batchInserter) flush(ctx context.Context) error {
	if b.rows == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString(b.prefix)
	for i := 0; i < b.rows; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(b.tuple)
	}
	_, err := b.tx.ExecContext(ctx, sb.String(), b.args...)
	b.args = b.args[:0]
	b.rows = 0
	return err
}

func ptrStr(p *string) any {
	if p == nil || *p == "" {
		return nil
	}
	return *p
}

func ptrInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func ptrFloat(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func ptrBool(p *bool) any {
	if p == nil {
		return nil
	}
	return boolInt(*p)
}
