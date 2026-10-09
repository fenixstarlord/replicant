package store

import (
	"context"
	"errors"
	"strings"
)

// Group is a user-defined set of drives (a shelf, a client, a year).
type Group struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// ListGroups returns groups by name with their drive counts.
func (s *Store) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT g.id, g.name, (SELECT count(*) FROM drives d WHERE d.group_id = g.id)
		FROM drive_groups g ORDER BY g.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Count); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetDriveGroup puts a drive in the named group, creating the group if
// needed. An empty name removes the drive from its group.
func (s *Store) SetDriveGroup(ctx context.Context, driveID int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		_, err := s.DB.ExecContext(ctx, `UPDATE drives SET group_id = NULL WHERE id = ?`, driveID)
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO drive_groups(name) VALUES (?) ON CONFLICT(name) DO NOTHING`, name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE drives SET group_id = (SELECT id FROM drive_groups WHERE name = ?) WHERE id = ?`, name, driveID); err != nil {
		return err
	}
	return tx.Commit()
}

// SetDriveGroupID moves a drive into a group by id; 0 ungroups it.
func (s *Store) SetDriveGroupID(ctx context.Context, driveID, groupID int64) error {
	if groupID == 0 {
		_, err := s.DB.ExecContext(ctx, `UPDATE drives SET group_id = NULL WHERE id = ?`, driveID)
		return err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE drives SET group_id = ? WHERE id = ? AND EXISTS (SELECT 1 FROM drive_groups WHERE id = ?)`, groupID, driveID, groupID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("no such drive or group")
	}
	return nil
}

// CreateGroup adds an empty group. Creating an existing name is a no-op.
func (s *Store) CreateGroup(ctx context.Context, name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errors.New("group name is required")
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO drive_groups(name) VALUES (?) ON CONFLICT(name) DO NOTHING`, name); err != nil {
		return 0, err
	}
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM drive_groups WHERE name = ?`, name).Scan(&id)
	return id, err
}

// DeleteGroup removes a group; its drives become ungrouped.
func (s *Store) DeleteGroup(ctx context.Context, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE drives SET group_id = NULL WHERE group_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM drive_groups WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// RenameGroup renames a group.
func (s *Store) RenameGroup(ctx context.Context, id int64, name string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE drive_groups SET name = ? WHERE id = ?`, strings.TrimSpace(name), id)
	return err
}
