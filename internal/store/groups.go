package store

import (
	"context"
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
// needed. An empty name removes the drive from its group. Groups left
// empty are deleted.
func (s *Store) SetDriveGroup(ctx context.Context, driveID int64, name string) error {
	name = strings.TrimSpace(name)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if name == "" {
		if _, err := tx.ExecContext(ctx, `UPDATE drives SET group_id = NULL WHERE id = ?`, driveID); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `INSERT INTO drive_groups(name) VALUES (?) ON CONFLICT(name) DO NOTHING`, name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE drives SET group_id = (SELECT id FROM drive_groups WHERE name = ?) WHERE id = ?`, name, driveID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM drive_groups WHERE id NOT IN (SELECT group_id FROM drives WHERE group_id IS NOT NULL)`); err != nil {
		return err
	}
	return tx.Commit()
}

// RenameGroup renames a group.
func (s *Store) RenameGroup(ctx context.Context, id int64, name string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE drive_groups SET name = ? WHERE id = ?`, strings.TrimSpace(name), id)
	return err
}
