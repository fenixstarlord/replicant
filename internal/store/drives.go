package store

import (
	"context"
	"database/sql"
	"time"
)

// Drive is a known drive with aggregates from its latest scan.
type Drive struct {
	ID            int64     `json:"id"`
	VolumeUUID    string    `json:"volume_uuid"`
	Name          string    `json:"name"`
	Label         string    `json:"label"`
	Location      string    `json:"location"`
	Notes         string    `json:"notes"`
	FSType        string    `json:"fs_type"`
	CapacityBytes int64     `json:"capacity_bytes"`
	FreeBytes     int64     `json:"free_bytes"`
	MediaName     string    `json:"media_name"`
	FirstSeen     time.Time `json:"first_seen"`
	LastSeen      time.Time `json:"last_seen"`
	ScanCount     int       `json:"scan_count"`
	FileCount     int       `json:"file_count"`
	ClipCount     int       `json:"clip_count"`
	TotalBytes    int64     `json:"total_bytes"`
	LastScannedAt time.Time `json:"last_scanned_at"`
	GroupID       int64     `json:"group_id"`
	GroupName     string    `json:"group_name"`
	ClientID      int64     `json:"client_id"`
	ClientName    string    `json:"client_name"`
}

// SetIn returns the drive's set id within a taxonomy.
func (d Drive) SetIn(t Taxonomy) int64 {
	if t == ByClient {
		return d.ClientID
	}
	return d.GroupID
}

// ListDrives returns every drive with latest-scan aggregates, by name.
func (s *Store) ListDrives(ctx context.Context) ([]Drive, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT d.id, d.volume_uuid, d.name, d.label, d.location, d.notes, d.fs_type,
		       d.capacity_bytes, d.free_bytes, d.media_name, d.first_seen, d.last_seen,
		       (SELECT count(*) FROM scans s WHERE s.drive_id = d.id),
		       coalesce(ls.file_count, 0), coalesce(ls.clip_count, 0), coalesce(ls.total_bytes, 0), ls.scanned_at,
		       coalesce(d.group_id, 0), coalesce(g.name, ''), coalesce(d.client_id, 0), coalesce(cl.name, '')
		FROM drives d
		LEFT JOIN drive_groups g ON g.id = d.group_id
		LEFT JOIN clients cl ON cl.id = d.client_id
		LEFT JOIN scans ls ON ls.id = (SELECT s.id FROM scans s WHERE s.drive_id = d.id AND s.is_latest = 1
		                               ORDER BY s.is_partial ASC, s.scanned_at DESC LIMIT 1)
		ORDER BY CASE WHEN g.name IS NULL THEN 1 ELSE 0 END, g.name COLLATE NOCASE, d.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Drive
	for rows.Next() {
		var d Drive
		var first, last string
		var scanned sql.NullString
		if err := rows.Scan(&d.ID, &d.VolumeUUID, &d.Name, &d.Label, &d.Location, &d.Notes, &d.FSType,
			&d.CapacityBytes, &d.FreeBytes, &d.MediaName, &first, &last,
			&d.ScanCount, &d.FileCount, &d.ClipCount, &d.TotalBytes, &scanned, &d.GroupID, &d.GroupName, &d.ClientID, &d.ClientName); err != nil {
			return nil, err
		}
		d.FirstSeen, _ = time.Parse(time.RFC3339, first)
		d.LastSeen, _ = time.Parse(time.RFC3339, last)
		if scanned.Valid {
			d.LastScannedAt, _ = time.Parse(time.RFC3339Nano, scanned.String)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpdateDriveLabels sets the user-editable fields of a drive.
func (s *Store) UpdateDriveLabels(ctx context.Context, id int64, label, location, notes string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE drives SET label = ?, location = ?, notes = ? WHERE id = ?`,
		label, location, notes, id)
	return err
}
