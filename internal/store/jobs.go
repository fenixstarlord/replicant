package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ScanJob is a path on the server that is scanned on demand or on a schedule.
type ScanJob struct {
	ID           int64         `json:"id"`
	Path         string        `json:"path"`
	Label        string        `json:"label"`
	IntervalMin  int           `json:"interval_min"` // 0 = manual only
	Extract      bool          `json:"extract"`
	Fingerprint  bool          `json:"fingerprint"`
	Enabled      bool          `json:"enabled"`
	CreatedAt    time.Time     `json:"created_at"`
	LastRunAt    *time.Time    `json:"last_run_at,omitempty"`
	LastStatus   string        `json:"last_status"` // "", running, ok, error
	LastError    string        `json:"last_error"`
	LastScanID   int64         `json:"last_scan_id"`
	LastDuration time.Duration `json:"last_duration"`
	NextRunAt    *time.Time    `json:"next_run_at,omitempty"`
}

const jobColumns = `id, path, label, interval_min, extract, fingerprint, enabled, created_at, last_run_at, last_status, last_error,
	coalesce(last_scan_id, 0), last_duration_ms, next_run_at FROM scan_jobs`

func scanJob(sc interface{ Scan(...any) error }) (ScanJob, error) {
	var j ScanJob
	var extract, fp, enabled int
	var created string
	var last, next sql.NullString
	var durMS int64
	if err := sc.Scan(&j.ID, &j.Path, &j.Label, &j.IntervalMin, &extract, &fp, &enabled, &created, &last, &j.LastStatus, &j.LastError,
		&j.LastScanID, &durMS, &next); err != nil {
		return j, err
	}
	j.Extract, j.Fingerprint, j.Enabled = extract == 1, fp == 1, enabled == 1
	j.CreatedAt, _ = time.Parse(time.RFC3339, created)
	j.LastDuration = time.Duration(durMS) * time.Millisecond
	if last.Valid {
		if t, err := time.Parse(time.RFC3339, last.String); err == nil {
			j.LastRunAt = &t
		}
	}
	if next.Valid {
		if t, err := time.Parse(time.RFC3339, next.String); err == nil {
			j.NextRunAt = &t
		}
	}
	return j, nil
}

// ListScanJobs returns all jobs by path.
func (s *Store) ListScanJobs(ctx context.Context) ([]ScanJob, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+jobColumns+" ORDER BY path")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScanJob
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// GetScanJobByPath returns the job for a path, or sql.ErrNoRows.
func (s *Store) GetScanJobByPath(ctx context.Context, path string) (ScanJob, error) {
	return scanJob(s.DB.QueryRowContext(ctx, "SELECT "+jobColumns+" WHERE path = ?", strings.TrimSpace(path)))
}

// GetScanJob returns one job.
func (s *Store) GetScanJob(ctx context.Context, id int64) (ScanJob, error) {
	return scanJob(s.DB.QueryRowContext(ctx, "SELECT "+jobColumns+" WHERE id = ?", id))
}

// CreateScanJob adds a job. A scheduled job is due immediately.
func (s *Store) CreateScanJob(ctx context.Context, j ScanJob) (int64, error) {
	j.Path = strings.TrimSpace(j.Path)
	if j.Path == "" {
		return 0, errors.New("path is required")
	}
	now := time.Now().UTC()
	var next any
	if j.IntervalMin > 0 && j.Enabled {
		next = now.Format(time.RFC3339)
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO scan_jobs(path, label, interval_min, extract, fingerprint, enabled, created_at, next_run_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, j.Path, strings.TrimSpace(j.Label), j.IntervalMin, boolInt(j.Extract), boolInt(j.Fingerprint), boolInt(j.Enabled),
		now.Format(time.RFC3339), next)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateScanJob changes a job's settings and recomputes its next run.
func (s *Store) UpdateScanJob(ctx context.Context, j ScanJob) error {
	var next any
	if j.IntervalMin > 0 && j.Enabled {
		base := time.Now().UTC()
		if j.LastRunAt != nil {
			base = j.LastRunAt.Add(time.Duration(j.IntervalMin) * time.Minute)
			if base.Before(time.Now().UTC()) {
				base = time.Now().UTC()
			}
		}
		next = base.Format(time.RFC3339)
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE scan_jobs SET label = ?, interval_min = ?, extract = ?, fingerprint = ?, enabled = ?, next_run_at = ? WHERE id = ?`,
		strings.TrimSpace(j.Label), j.IntervalMin, boolInt(j.Extract), boolInt(j.Fingerprint), boolInt(j.Enabled), next, j.ID)
	return err
}

// DeleteScanJob removes a job.
func (s *Store) DeleteScanJob(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM scan_jobs WHERE id = ?`, id)
	return err
}

// DueScanJobs returns enabled scheduled jobs whose next run has passed.
func (s *Store) DueScanJobs(ctx context.Context, now time.Time) ([]ScanJob, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+jobColumns+
		" WHERE enabled = 1 AND interval_min > 0 AND last_status != 'running' AND (next_run_at IS NULL OR next_run_at <= ?) ORDER BY path",
		now.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScanJob
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// MarkScanJobRunning records that a run started.
func (s *Store) MarkScanJobRunning(ctx context.Context, id int64, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE scan_jobs SET last_status = 'running', last_error = '', last_run_at = ? WHERE id = ?`,
		now.UTC().Format(time.RFC3339), id)
	return err
}

// MarkScanJobDone records the outcome of a run and schedules the next one.
func (s *Store) MarkScanJobDone(ctx context.Context, id int64, runErr error, scanID int64, took time.Duration, now time.Time) error {
	j, err := s.GetScanJob(ctx, id)
	if err != nil {
		return err
	}
	status, msg := "ok", ""
	if runErr != nil {
		status, msg = "error", runErr.Error()
	}
	var next any
	if j.IntervalMin > 0 && j.Enabled {
		next = now.UTC().Add(time.Duration(j.IntervalMin) * time.Minute).Format(time.RFC3339)
	}
	var sid any
	if scanID > 0 {
		sid = scanID
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE scan_jobs SET last_status = ?, last_error = ?, last_scan_id = coalesce(?, last_scan_id),
		last_duration_ms = ?, next_run_at = ? WHERE id = ?`, status, msg, sid, took.Milliseconds(), next, id)
	return err
}

// ResetRunningScanJobs clears "running" left behind by a crash.
func (s *Store) ResetRunningScanJobs(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE scan_jobs SET last_status = 'error', last_error = 'interrupted by server restart' WHERE last_status = 'running'`)
	return err
}
