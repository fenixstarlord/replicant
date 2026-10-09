package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fenixstarlord/replicant/internal/meta"
)

// ClipRow is a clip as shown in search results and detail pages.
type ClipRow struct {
	ID         int64             `json:"id"`
	ScanID     int64             `json:"scan_id"`
	DriveID    int64             `json:"drive_id"`
	DriveName  string            `json:"drive_name"`
	DriveLabel string            `json:"drive_label"`
	Location   string            `json:"location"`
	IsLatest   bool              `json:"is_latest"`
	ScannedAt  time.Time         `json:"scanned_at"`
	ClipKey    string            `json:"clip_key"`
	RootPath   string            `json:"root_path"`
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	FileCount  int               `json:"file_count"`
	TotalSize  int64             `json:"total_size"`
	ModTime    time.Time         `json:"mtime"`
	Files      []string          `json:"files"`
	Sidecars   []string          `json:"sidecars"`
	SeqFrames  int64             `json:"seq_frames"`
	Meta       meta.Fields       `json:"meta"`
	Sources    map[string]string `json:"sources"`
	Errors     []string          `json:"errors"`
}

// Dir returns the parent directory of the clip's root path.
func (c ClipRow) Dir() string { return parentOf(c.RootPath) }

const clipColumns = `c.id, c.scan_id, s.drive_id, d.name, d.label, d.location, s.is_latest, s.scanned_at,
	c.clip_key, c.root_path, c.name, c.kind, c.file_count, c.total_size, c.mtime, c.files_json, c.sidecars_json,
	coalesce(c.seq_frame_count, 0),
	c.clip_name, c.reel, c.camera_index, c.scene, c.take, c.circled, c.recorded_at, c.tc_start, c.tc_end, c.duration_s, c.frame_count,
	c.container, c.codec, c.codec_detail, c.width, c.height, c.sensor_mode, c.fps, c.capture_fps, c.bit_depth, c.color_gamma, c.squeeze,
	c.camera_make, c.camera_model, c.camera_serial, c.firmware, c.iso, c.wb_kelvin, c.tint, c.shutter_angle, c.shutter_speed, c.nd,
	c.lens, c.focal_mm, c.t_stop, c.focus_distance, c.audio_channels, c.sample_rate, c.audio_bit_depth, c.look,
	c.field_sources_json, c.errors_json`

const clipFrom = ` FROM clips c JOIN scans s ON s.id = c.scan_id JOIN drives d ON d.id = s.drive_id `

func scanClipRow(sc interface{ Scan(...any) error }) (ClipRow, error) {
	var r ClipRow
	var latest int
	var scanned, mtime, filesJSON, sidecarsJSON, sourcesJSON, errorsJSON string
	var clipName, reel, camIdx, scene, take, recorded, tcStart, tcEnd, container, codec, codecDetail, sensorMode,
		colorGamma, make, model, serial, firmware, shutterSpeed, nd, lens, focus, look sql.NullString
	var circled, frameCount, width, height, bitDepth, iso, wb, audioCh, sampleRate, audioBits sql.NullInt64
	var duration, fps, captureFPS, squeeze, tint, shutterAngle, focal, tstop sql.NullFloat64
	var mtimeN sql.NullString
	err := sc.Scan(&r.ID, &r.ScanID, &r.DriveID, &r.DriveName, &r.DriveLabel, &r.Location, &latest, &scanned,
		&r.ClipKey, &r.RootPath, &r.Name, &r.Kind, &r.FileCount, &r.TotalSize, &mtimeN, &filesJSON, &sidecarsJSON,
		&r.SeqFrames,
		&clipName, &reel, &camIdx, &scene, &take, &circled, &recorded, &tcStart, &tcEnd, &duration, &frameCount,
		&container, &codec, &codecDetail, &width, &height, &sensorMode, &fps, &captureFPS, &bitDepth, &colorGamma, &squeeze,
		&make, &model, &serial, &firmware, &iso, &wb, &tint, &shutterAngle, &shutterSpeed, &nd,
		&lens, &focal, &tstop, &focus, &audioCh, &sampleRate, &audioBits, &look,
		&sourcesJSON, &errorsJSON)
	if err != nil {
		return r, err
	}
	r.IsLatest = latest == 1
	r.ScannedAt, _ = time.Parse(time.RFC3339Nano, scanned)
	if mtimeN.Valid {
		mtime = mtimeN.String
		r.ModTime, _ = time.Parse(time.RFC3339Nano, mtime)
	}
	json.Unmarshal([]byte(filesJSON), &r.Files)       //nolint:errcheck
	json.Unmarshal([]byte(sidecarsJSON), &r.Sidecars) //nolint:errcheck
	json.Unmarshal([]byte(sourcesJSON), &r.Sources)   //nolint:errcheck
	json.Unmarshal([]byte(errorsJSON), &r.Errors)     //nolint:errcheck
	m := &r.Meta
	m.ClipName, m.Reel, m.CameraIndex, m.Scene, m.Take = ns(clipName), ns(reel), ns(camIdx), ns(scene), ns(take)
	if circled.Valid {
		m.Circled = meta.Bool(circled.Int64 == 1)
	}
	if recorded.Valid {
		if t, err := time.Parse(time.RFC3339Nano, recorded.String); err == nil {
			m.RecordedAt = &t
		}
	}
	m.TCStart, m.TCEnd = ns(tcStart), ns(tcEnd)
	m.DurationS, m.FrameCount = nf(duration), ni(frameCount)
	m.Container, m.Codec, m.CodecDetail = ns(container), ns(codec), ns(codecDetail)
	m.Width, m.Height, m.SensorMode = ni(width), ni(height), ns(sensorMode)
	m.FPS, m.CaptureFPS, m.BitDepth, m.ColorGamma, m.Squeeze = nf(fps), nf(captureFPS), ni(bitDepth), ns(colorGamma), nf(squeeze)
	m.CameraMake, m.CameraModel, m.CameraSerial, m.Firmware = ns(make), ns(model), ns(serial), ns(firmware)
	m.ISO, m.WBKelvin, m.Tint, m.ShutterAngle, m.ShutterSpeed, m.ND = ni(iso), ni(wb), nf(tint), nf(shutterAngle), ns(shutterSpeed), ns(nd)
	m.Lens, m.FocalMM, m.TStop, m.FocusDistance = ns(lens), nf(focal), nf(tstop), ns(focus)
	m.AudioChannels, m.SampleRate, m.AudioBitDepth, m.Look = ni(audioCh), ni(sampleRate), ni(audioBits), ns(look)
	return r, nil
}

func ns(v sql.NullString) *string {
	if !v.Valid || v.String == "" {
		return nil
	}
	return &v.String
}
func ni(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}
func nf(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}

// SearchQuery describes a clip search. Zero values mean "no filter".
type SearchQuery struct {
	Text       string
	DriveIDs   []int64
	Kind       string
	Codec      string
	Camera     string
	ColorGamma string
	Lens       string
	Reel       string
	Scene      string
	Take       string
	Timecode   string
	MinWidth   int64
	MinISO     int64
	MaxISO     int64
	MinWB      int64
	MaxWB      int64
	FPS        float64
	MinFocal   float64
	MaxFocal   float64
	MinDur     float64
	MaxDur     float64
	MinSize    int64
	MaxSize    int64
	From       time.Time
	To         time.Time
	IncludeOld bool
	Sort       string
	Desc       bool
	Limit      int
	Offset     int
}

// clipSorts whitelists sortable columns.
var clipSorts = map[string]string{
	"name": "c.name", "drive": "d.name", "path": "c.root_path", "codec": "c.codec", "resolution": "c.width",
	"fps": "c.fps", "duration": "c.duration_s", "camera": "c.camera_model", "reel": "c.reel", "size": "c.total_size",
	"recorded": "c.recorded_at", "iso": "c.iso", "scene": "c.scene", "mtime": "c.mtime",
}

type where struct {
	clauses []string
	args    []any
}

func (w *where) add(clause string, args ...any) {
	w.clauses = append(w.clauses, clause)
	w.args = append(w.args, args...)
}

func (w *where) sql() string {
	if len(w.clauses) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(w.clauses, " AND ")
}

// ftsPhrase quotes text for an FTS5 MATCH as a single phrase.
func ftsPhrase(text string) string {
	return `"` + strings.ReplaceAll(text, `"`, `""`) + `"`
}

func likeArg(s string) string {
	return "%" + strings.NewReplacer("%", `\%`, "_", `\_`).Replace(s) + "%"
}

func (q SearchQuery) where() *where {
	w := &where{}
	if !q.IncludeOld {
		w.add("s.is_latest = 1")
	}
	if text := strings.TrimSpace(q.Text); text != "" {
		if len([]rune(text)) >= 3 {
			w.add("c.id IN (SELECT clip_id FROM clips_fts WHERE clips_fts MATCH ?)", ftsPhrase(text))
		} else {
			w.add(`(c.name LIKE ? ESCAPE '\' OR c.root_path LIKE ? ESCAPE '\')`, likeArg(text), likeArg(text))
		}
	}
	if len(q.DriveIDs) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(q.DriveIDs)), ",")
		args := make([]any, len(q.DriveIDs))
		for i, id := range q.DriveIDs {
			args[i] = id
		}
		w.add("s.drive_id IN ("+ph+")", args...)
	}
	eq := func(col, v string) {
		if v != "" {
			w.add(col+" = ?", v)
		}
	}
	like := func(col, v string) {
		if v != "" {
			w.add(col+` LIKE ? ESCAPE '\'`, likeArg(v))
		}
	}
	eq("c.kind", q.Kind)
	eq("c.codec", q.Codec)
	eq("c.camera_model", q.Camera)
	eq("c.color_gamma", q.ColorGamma)
	like("c.lens", q.Lens)
	like("c.reel", q.Reel)
	eq("c.scene", q.Scene)
	eq("c.take", q.Take)
	if q.Timecode != "" {
		w.add(`(c.tc_start LIKE ? ESCAPE '\' OR c.tc_end LIKE ? ESCAPE '\')`, likeArg(q.Timecode), likeArg(q.Timecode))
	}
	if q.MinWidth > 0 {
		w.add("c.width >= ?", q.MinWidth)
	}
	if q.MinISO > 0 {
		w.add("c.iso >= ?", q.MinISO)
	}
	if q.MaxISO > 0 {
		w.add("c.iso <= ?", q.MaxISO)
	}
	if q.MinWB > 0 {
		w.add("c.wb_kelvin >= ?", q.MinWB)
	}
	if q.MaxWB > 0 {
		w.add("c.wb_kelvin <= ?", q.MaxWB)
	}
	if q.FPS > 0 {
		w.add("abs(c.fps - ?) < 0.01", q.FPS)
	}
	if q.MinFocal > 0 {
		w.add("c.focal_mm >= ?", q.MinFocal)
	}
	if q.MaxFocal > 0 {
		w.add("c.focal_mm <= ?", q.MaxFocal)
	}
	if q.MinDur > 0 {
		w.add("c.duration_s >= ?", q.MinDur)
	}
	if q.MaxDur > 0 {
		w.add("c.duration_s <= ?", q.MaxDur)
	}
	if q.MinSize > 0 {
		w.add("c.total_size >= ?", q.MinSize)
	}
	if q.MaxSize > 0 {
		w.add("c.total_size <= ?", q.MaxSize)
	}
	if !q.From.IsZero() {
		w.add("c.recorded_at >= ?", q.From.UTC().Format(time.RFC3339Nano))
	}
	if !q.To.IsZero() {
		w.add("c.recorded_at < ?", q.To.UTC().Format(time.RFC3339Nano))
	}
	return w
}

func (q SearchQuery) orderBy() string {
	col, ok := clipSorts[q.Sort]
	if !ok {
		col = "c.name"
	}
	dir := "ASC"
	if q.Desc {
		dir = "DESC"
	}
	return fmt.Sprintf(" ORDER BY %s %s NULLS LAST, c.id %s", col, dir, dir)
}

// SearchClips returns matching clips and the total match count.
func (s *Store) SearchClips(ctx context.Context, q SearchQuery) ([]ClipRow, int, error) {
	w := q.where()
	var total int
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*)"+clipFrom+w.sql(), w.args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	args := append(append([]any{}, w.args...), limit, q.Offset)
	rows, err := s.DB.QueryContext(ctx, "SELECT "+clipColumns+clipFrom+w.sql()+q.orderBy()+" LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []ClipRow
	for rows.Next() {
		r, err := scanClipRow(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// EntryRow is a file or directory as shown in browse and file search.
type EntryRow struct {
	ID           int64     `json:"id"`
	ScanID       int64     `json:"scan_id"`
	DriveID      int64     `json:"drive_id"`
	DriveName    string    `json:"drive_name"`
	IsLatest     bool      `json:"is_latest"`
	ParentPath   string    `json:"parent_path"`
	Path         string    `json:"path"`
	Name         string    `json:"name"`
	Ext          string    `json:"ext"`
	Kind         string    `json:"kind"`
	IsDir        bool      `json:"is_dir"`
	IsPackage    bool      `json:"is_package"`
	IsSymlink    bool      `json:"is_symlink"`
	Size         int64     `json:"size"`
	ModTime      time.Time `json:"mtime"`
	BirthTime    time.Time `json:"btime"`
	Fingerprint  string    `json:"fingerprint"`
	FullHash     string    `json:"full_hash"`
	ClipKey      string    `json:"clip_key"`
	ClipID       int64     `json:"clip_id"` // 0 if none
	ClipKind     string    `json:"clip_kind"`
	Error        string    `json:"error"`
	DirTotalSize int64     `json:"dir_total_size"`
	DirFileCount int64     `json:"dir_file_count"`
}

const entryColumns = `e.id, e.scan_id, s.drive_id, d.name, s.is_latest, e.parent_path, e.path, e.name, e.ext, e.kind,
	e.is_dir, e.is_package, e.is_symlink, e.size, coalesce(e.mtime,''), coalesce(e.btime,''), coalesce(e.fingerprint,''),
	coalesce(e.full_hash,''), coalesce(e.clip_key,''), coalesce(c.id,0), coalesce(c.kind,''), coalesce(e.error,''),
	coalesce(e.dir_total_size,0), coalesce(e.dir_file_count,0)`

const entryFrom = ` FROM entries e JOIN scans s ON s.id = e.scan_id JOIN drives d ON d.id = s.drive_id
	LEFT JOIN clips c ON c.scan_id = e.scan_id AND c.clip_key = e.clip_key `

func scanEntryRow(sc interface{ Scan(...any) error }) (EntryRow, error) {
	var r EntryRow
	var latest, isDir, isPkg, isLink int
	var mtime, btime string
	if err := sc.Scan(&r.ID, &r.ScanID, &r.DriveID, &r.DriveName, &latest, &r.ParentPath, &r.Path, &r.Name, &r.Ext, &r.Kind,
		&isDir, &isPkg, &isLink, &r.Size, &mtime, &btime, &r.Fingerprint, &r.FullHash, &r.ClipKey, &r.ClipID, &r.ClipKind,
		&r.Error, &r.DirTotalSize, &r.DirFileCount); err != nil {
		return r, err
	}
	r.IsLatest, r.IsDir, r.IsPackage, r.IsSymlink = latest == 1, isDir == 1, isPkg == 1, isLink == 1
	r.ModTime, _ = time.Parse(time.RFC3339Nano, mtime)
	r.BirthTime, _ = time.Parse(time.RFC3339Nano, btime)
	return r, nil
}

// ListDir returns the entries directly inside parent for a scan, directories first.
func (s *Store) ListDir(ctx context.Context, scanID int64, parent string) ([]EntryRow, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+entryColumns+entryFrom+
		" WHERE e.scan_id = ? AND e.parent_path = ? ORDER BY e.is_dir DESC, e.name COLLATE NOCASE", scanID, parent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EntryRow
	for rows.Next() {
		r, err := scanEntryRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// EntrySearchQuery describes a file search.
type EntrySearchQuery struct {
	Text       string
	DriveIDs   []int64
	Kind       string
	Ext        string
	MinSize    int64
	MaxSize    int64
	IncludeOld bool
	Limit      int
	Offset     int
}

// SearchEntries searches file and directory names (trigram FTS), or the
// full path with LIKE when the text contains a slash.
func (s *Store) SearchEntries(ctx context.Context, q EntrySearchQuery) ([]EntryRow, int, error) {
	w := &where{}
	if !q.IncludeOld {
		w.add("s.is_latest = 1")
	}
	text := strings.TrimSpace(q.Text)
	switch {
	case text == "":
	case strings.Contains(text, "/") || len([]rune(text)) < 3:
		w.add(`e.path LIKE ? ESCAPE '\'`, likeArg(text))
	default:
		w.add("e.id IN (SELECT rowid FROM entries_fts WHERE entries_fts MATCH ?)", ftsPhrase(text))
	}
	if len(q.DriveIDs) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(q.DriveIDs)), ",")
		args := make([]any, len(q.DriveIDs))
		for i, id := range q.DriveIDs {
			args[i] = id
		}
		w.add("s.drive_id IN ("+ph+")", args...)
	}
	if q.Kind != "" {
		w.add("e.kind = ?", q.Kind)
	}
	if q.Ext != "" {
		w.add("e.ext = ?", strings.ToLower(strings.TrimPrefix(q.Ext, ".")))
	}
	if q.MinSize > 0 {
		w.add("e.size >= ?", q.MinSize)
	}
	if q.MaxSize > 0 {
		w.add("e.size <= ?", q.MaxSize)
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*)"+entryFrom+w.sql(), w.args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	args := append(append([]any{}, w.args...), limit, q.Offset)
	rows, err := s.DB.QueryContext(ctx, "SELECT "+entryColumns+entryFrom+w.sql()+
		" ORDER BY e.is_dir DESC, e.name COLLATE NOCASE, e.id LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []EntryRow
	for rows.Next() {
		r, err := scanEntryRow(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// GetClip returns one clip with its raw extractor output.
func (s *Store) GetClip(ctx context.Context, id int64) (ClipRow, map[string]json.RawMessage, error) {
	r, err := scanClipRow(s.DB.QueryRowContext(ctx, "SELECT "+clipColumns+clipFrom+" WHERE c.id = ?", id))
	if err != nil {
		return r, nil, err
	}
	raw := map[string]json.RawMessage{}
	rows, err := s.DB.QueryContext(ctx, `SELECT source, raw_json FROM clip_raw WHERE clip_id = ? ORDER BY source`, id)
	if err != nil {
		return r, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var src, js string
		if err := rows.Scan(&src, &js); err != nil {
			return r, nil, err
		}
		raw[src] = json.RawMessage(js)
	}
	return r, raw, rows.Err()
}

// GetEntry returns one entry.
func (s *Store) GetEntry(ctx context.Context, id int64) (EntryRow, error) {
	return scanEntryRow(s.DB.QueryRowContext(ctx, "SELECT "+entryColumns+entryFrom+" WHERE e.id = ?", id))
}

// ClipHistory returns this clip's root path across all scans of its drive, newest first.
func (s *Store) ClipHistory(ctx context.Context, driveID int64, rootPath string) ([]ClipRow, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+clipColumns+clipFrom+
		" WHERE s.drive_id = ? AND c.root_path = ? ORDER BY s.scanned_at DESC", driveID, rootPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClipRow
	for rows.Next() {
		r, err := scanClipRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Copies finds entries in the latest scans of other drives (or elsewhere
// on the same drive) with the same fingerprint, or the same name and
// size when no fingerprint is known.
func (s *Store) Copies(ctx context.Context, e EntryRow) ([]EntryRow, error) {
	var q string
	var args []any
	if e.Fingerprint != "" {
		q = " WHERE s.is_latest = 1 AND e.fingerprint = ? AND e.id != ?"
		args = []any{e.Fingerprint, e.ID}
	} else {
		q = " WHERE s.is_latest = 1 AND e.name = ? AND e.size = ? AND e.is_dir = 0 AND e.id != ?"
		args = []any{e.Name, e.Size, e.ID}
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT "+entryColumns+entryFrom+q+" ORDER BY d.name, e.path LIMIT 200", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EntryRow
	for rows.Next() {
		r, err := scanEntryRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// EntryByPath finds an entry by scan and path.
func (s *Store) EntryByPath(ctx context.Context, scanID int64, path string) (EntryRow, error) {
	return scanEntryRow(s.DB.QueryRowContext(ctx, "SELECT "+entryColumns+entryFrom+" WHERE e.scan_id = ? AND e.path = ?", scanID, path))
}

// Scan is a scan row.
type Scan struct {
	ID             int64     `json:"id"`
	DriveID        int64     `json:"drive_id"`
	DriveName      string    `json:"drive_name"`
	ScannedAt      time.Time `json:"scanned_at"`
	IngestedAt     time.Time `json:"ingested_at"`
	ScannerVersion string    `json:"scanner_version"`
	Root           string    `json:"root"`
	FileCount      int       `json:"file_count"`
	DirCount       int       `json:"dir_count"`
	ClipCount      int       `json:"clip_count"`
	TotalBytes     int64     `json:"total_bytes"`
	Added          int       `json:"added"`
	Removed        int       `json:"removed"`
	Changed        int       `json:"changed"`
	IsLatest       bool      `json:"is_latest"`
	IsPartial      bool      `json:"is_partial"`
	Extractors     string    `json:"extractors_json"`
}

const scanColumns = `s.id, s.drive_id, d.name, s.scanned_at, s.ingested_at, s.scanner_version, s.root, s.file_count, s.dir_count,
	s.clip_count, s.total_bytes, s.added, s.removed, s.changed, s.is_latest, s.is_partial, s.extractors_json FROM scans s JOIN drives d ON d.id = s.drive_id`

func scanScan(sc interface{ Scan(...any) error }) (Scan, error) {
	var r Scan
	var scanned, ingested string
	var latest, partial int
	if err := sc.Scan(&r.ID, &r.DriveID, &r.DriveName, &scanned, &ingested, &r.ScannerVersion, &r.Root, &r.FileCount, &r.DirCount,
		&r.ClipCount, &r.TotalBytes, &r.Added, &r.Removed, &r.Changed, &latest, &partial, &r.Extractors); err != nil {
		return r, err
	}
	r.ScannedAt, _ = time.Parse(time.RFC3339Nano, scanned)
	r.IngestedAt, _ = time.Parse(time.RFC3339, ingested)
	r.IsLatest = latest == 1
	r.IsPartial = partial == 1
	return r, nil
}

// ListScans returns a drive's scans, newest first.
func (s *Store) ListScans(ctx context.Context, driveID int64) ([]Scan, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+scanColumns+" WHERE s.drive_id = ? ORDER BY s.scanned_at DESC", driveID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Scan
	for rows.Next() {
		r, err := scanScan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetScan returns one scan.
func (s *Store) GetScan(ctx context.Context, id int64) (Scan, error) {
	return scanScan(s.DB.QueryRowContext(ctx, "SELECT "+scanColumns+" WHERE s.id = ?", id))
}

// LatestScan returns a drive's latest full scan, or the newest latest
// partial scan when the drive has never been scanned whole.
func (s *Store) LatestScan(ctx context.Context, driveID int64) (Scan, error) {
	return scanScan(s.DB.QueryRowContext(ctx, "SELECT "+scanColumns+
		" WHERE s.drive_id = ? AND s.is_latest = 1 ORDER BY s.is_partial ASC, s.scanned_at DESC LIMIT 1", driveID))
}

// GetDrive returns one drive with aggregates.
func (s *Store) GetDrive(ctx context.Context, id int64) (Drive, error) {
	drives, err := s.ListDrives(ctx)
	if err != nil {
		return Drive{}, err
	}
	for _, d := range drives {
		if d.ID == id {
			return d, nil
		}
	}
	return Drive{}, sql.ErrNoRows
}

// Facets lists distinct filter values across latest scans.
type Facets struct {
	Kinds   []string
	Codecs  []string
	Cameras []string
	Gammas  []string
	Reels   []string
}

// Facets returns the distinct values used to populate filter dropdowns.
func (s *Store) Facets(ctx context.Context) (Facets, error) {
	var f Facets
	distinct := func(col string) ([]string, error) {
		rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT c.`+col+` FROM clips c JOIN scans s ON s.id = c.scan_id
			WHERE s.is_latest = 1 AND c.`+col+` IS NOT NULL AND c.`+col+` != '' ORDER BY 1 LIMIT 500`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, rows.Err()
	}
	var err error
	if f.Kinds, err = distinct("kind"); err != nil {
		return f, err
	}
	if f.Codecs, err = distinct("codec"); err != nil {
		return f, err
	}
	if f.Cameras, err = distinct("camera_model"); err != nil {
		return f, err
	}
	if f.Gammas, err = distinct("color_gamma"); err != nil {
		return f, err
	}
	if f.Reels, err = distinct("reel"); err != nil {
		return f, err
	}
	return f, nil
}

// Change is one row of scan_changes.
type Change struct {
	Path    string `json:"path"`
	Change  string `json:"change"`
	OldSize int64  `json:"old_size"`
	NewSize int64  `json:"new_size"`
}

// ScanChanges lists a scan's recorded differences from the previous scan,
// optionally filtered to one change type ("added", "removed", "changed").
// It returns the page and the total count for the filter.
func (s *Store) ScanChanges(ctx context.Context, scanID int64, change string, limit, offset int) ([]Change, int, error) {
	w := &where{}
	w.add("scan_id = ?", scanID)
	if change != "" {
		w.add("change = ?", change)
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM scan_changes"+w.sql(), w.args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 500
	}
	args := append(append([]any{}, w.args...), limit, offset)
	rows, err := s.DB.QueryContext(ctx, `SELECT path, change, coalesce(old_size,0), coalesce(new_size,0)
		FROM scan_changes`+w.sql()+` ORDER BY change, path LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Change
	for rows.Next() {
		var c Change
		if err := rows.Scan(&c.Path, &c.Change, &c.OldSize, &c.NewSize); err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}
