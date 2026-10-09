package store

import (
	"context"
	"database/sql"
	"time"
)

// Activity is one scan in progress or finished: who ran it, on what, and
// how far it got. Clients report theirs over the API; the server's own
// scans report directly. Finished rows are the scan history.
type Activity struct {
	ID         int64      `json:"id"`
	Host       string     `json:"host"`
	Source     string     `json:"source"` // API key name, "server", "file", or "local"
	DriveName  string     `json:"drive_name"`
	VolumeUUID string     `json:"volume_uuid"`
	Root       string     `json:"root"`
	Stage      string     `json:"stage"`
	Done       int        `json:"done"`
	Total      int        `json:"total"`
	Status     string     `json:"status"` // running, done, error, cancelled, lost
	Error      string     `json:"error,omitempty"`
	ScanID     *int64     `json:"scan_id,omitempty"`
	DriveID    *int64     `json:"drive_id,omitempty"` // from the stored scan, when finished
	StartedAt  time.Time  `json:"started_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Running reports whether the activity is still in progress.
func (a Activity) Running() bool { return a.Status == "running" }

// Duration is how long the activity ran, or has been running.
func (a Activity) Duration() time.Duration {
	if a.FinishedAt != nil {
		return a.FinishedAt.Sub(a.StartedAt)
	}
	return time.Since(a.StartedAt)
}

// Percent is progress for the current stage, or -1 when unknown.
func (a Activity) Percent() int {
	if a.Total <= 0 {
		return -1
	}
	return a.Done * 100 / a.Total
}

// staleAfter is how long a running activity may go without an update
// before it is presumed lost (a client that crashed or lost the network).
const staleAfter = 5 * time.Minute

// StartActivity records a scan that has just begun and returns its id.
func (s *Store) StartActivity(ctx context.Context, a Activity) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx, `INSERT INTO activity(host, source, drive_name, volume_uuid, root, stage, status, started_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'starting', 'running', ?, ?)`, a.Host, a.Source, a.DriveName, a.VolumeUUID, a.Root, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateActivity records progress. Empty fields are left alone.
func (s *Store) UpdateActivity(ctx context.Context, id int64, stage string, done, total int, driveName, volumeUUID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx, `UPDATE activity SET stage = CASE WHEN ? = '' THEN stage ELSE ? END, done = ?, total = ?,
		drive_name = CASE WHEN ? = '' THEN drive_name ELSE ? END, volume_uuid = CASE WHEN ? = '' THEN volume_uuid ELSE ? END,
		updated_at = ? WHERE id = ? AND status = 'running'`,
		stage, stage, done, total, driveName, driveName, volumeUUID, volumeUUID, now, id)
	return err
}

// FinishActivity closes an activity: status done (with the stored scan),
// error, or cancelled.
func (s *Store) FinishActivity(ctx context.Context, id int64, status string, scanID int64, errMsg string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	var sid any
	if scanID > 0 {
		sid = scanID
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE activity SET status = ?, error = ?, scan_id = ?, stage = CASE WHEN ? = 'done' THEN 'ingest' ELSE stage END,
		updated_at = ?, finished_at = ? WHERE id = ? AND status = 'running'`, status, errMsg, sid, status, now, now, id)
	return err
}

// ExpireActivity marks running activities that have not reported for a
// while as lost, so a crashed client does not show as scanning forever.
func (s *Store) ExpireActivity(ctx context.Context) error {
	cutoff := time.Now().Add(-staleAfter).UTC().Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx, `UPDATE activity SET status = 'lost', error = 'no progress reported for 5 minutes', finished_at = ?
		WHERE status = 'running' AND updated_at < ?`, now, cutoff)
	return err
}

// LoseServerActivity marks the server's own running activities as lost;
// called at startup, since a restart killed them.
func (s *Store) LoseServerActivity(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx, `UPDATE activity SET status = 'lost', error = 'server restarted', finished_at = ?
		WHERE status = 'running' AND source = 'server'`, now)
	return err
}

const activityColumns = `a.id, a.host, a.source, a.drive_name, a.volume_uuid, a.root, a.stage, a.done, a.total, a.status, a.error,
	a.scan_id, s.drive_id, a.started_at, a.updated_at, a.finished_at FROM activity a LEFT JOIN scans s ON s.id = a.scan_id`

func scanActivity(rows *sql.Rows) (Activity, error) {
	var a Activity
	var scanID, driveID sql.NullInt64
	var started, updated string
	var finished sql.NullString
	if err := rows.Scan(&a.ID, &a.Host, &a.Source, &a.DriveName, &a.VolumeUUID, &a.Root, &a.Stage, &a.Done, &a.Total, &a.Status, &a.Error,
		&scanID, &driveID, &started, &updated, &finished); err != nil {
		return a, err
	}
	if scanID.Valid {
		a.ScanID = &scanID.Int64
	}
	if driveID.Valid {
		a.DriveID = &driveID.Int64
	}
	a.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
	a.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	if finished.Valid {
		t, _ := time.Parse(time.RFC3339, finished.String)
		a.FinishedAt = &t
	}
	return a, nil
}

func (s *Store) queryActivity(ctx context.Context, where string, args ...any) ([]Activity, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+activityColumns+" "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Activity
	for rows.Next() {
		a, err := scanActivity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// RunningActivity lists scans in progress, oldest first.
func (s *Store) RunningActivity(ctx context.Context) ([]Activity, error) {
	return s.queryActivity(ctx, `WHERE a.status = 'running' ORDER BY a.started_at`)
}

// ListActivity lists activity newest first, running rows included.
func (s *Store) ListActivity(ctx context.Context, limit, offset int) ([]Activity, int, error) {
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM activity`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.queryActivity(ctx, `ORDER BY (a.status = 'running') DESC, a.started_at DESC LIMIT ? OFFSET ?`, limit, offset)
	return rows, total, err
}

// LastFinishedActivity returns the most recently finished activity, if any.
func (s *Store) LastFinishedActivity(ctx context.Context) (Activity, bool, error) {
	rows, err := s.queryActivity(ctx, `WHERE a.status != 'running' ORDER BY a.finished_at DESC LIMIT 1`)
	if err != nil || len(rows) == 0 {
		return Activity{}, false, err
	}
	return rows[0], true, nil
}
