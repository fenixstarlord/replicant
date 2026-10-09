package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Taxonomy is one way of organising drives. Groups are physical or
// project sets (a shelf, a box, a year); clients are who the drives
// belong to. A drive can be in one of each.
type Taxonomy string

// The two taxonomies.
const (
	ByGroup  Taxonomy = "group"
	ByClient Taxonomy = "client"
)

// Valid reports whether t is a known taxonomy.
func (t Taxonomy) Valid() bool { return t == ByGroup || t == ByClient }

func (t Taxonomy) table() string {
	if t == ByClient {
		return "clients"
	}
	return "drive_groups"
}

func (t Taxonomy) column() string {
	if t == ByClient {
		return "client_id"
	}
	return "group_id"
}

// Group is a named set of drives within a taxonomy.
type Group struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// ListGroups returns the taxonomy's sets by name with their drive counts.
func (s *Store) ListGroups(ctx context.Context, t Taxonomy) ([]Group, error) {
	rows, err := s.DB.QueryContext(ctx, fmt.Sprintf(`SELECT g.id, g.name, (SELECT count(*) FROM drives d WHERE d.%s = g.id)
		FROM %s g ORDER BY g.name COLLATE NOCASE`, t.column(), t.table()))
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

// SetDriveGroup puts a drive in the named set, creating it if needed. An
// empty name removes the drive from the taxonomy.
func (s *Store) SetDriveGroup(ctx context.Context, t Taxonomy, driveID int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		_, err := s.DB.ExecContext(ctx, fmt.Sprintf(`UPDATE drives SET %s = NULL WHERE id = ?`, t.column()), driveID)
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s(name) VALUES (?) ON CONFLICT(name) DO NOTHING`, t.table()), name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE drives SET %s = (SELECT id FROM %s WHERE name = ?) WHERE id = ?`, t.column(), t.table()), name, driveID); err != nil {
		return err
	}
	return tx.Commit()
}

// SetDriveGroupID moves a drive into a set by id; 0 removes it.
func (s *Store) SetDriveGroupID(ctx context.Context, t Taxonomy, driveID, id int64) error {
	if id == 0 {
		_, err := s.DB.ExecContext(ctx, fmt.Sprintf(`UPDATE drives SET %s = NULL WHERE id = ?`, t.column()), driveID)
		return err
	}
	res, err := s.DB.ExecContext(ctx, fmt.Sprintf(`UPDATE drives SET %s = ? WHERE id = ? AND EXISTS (SELECT 1 FROM %s WHERE id = ?)`, t.column(), t.table()), id, driveID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("no such drive or " + string(t))
	}
	return nil
}

// CreateGroup adds an empty set. Creating an existing name returns it.
func (s *Store) CreateGroup(ctx context.Context, t Taxonomy, name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errors.New(string(t) + " name is required")
	}
	if _, err := s.DB.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s(name) VALUES (?) ON CONFLICT(name) DO NOTHING`, t.table()), name); err != nil {
		return 0, err
	}
	var id int64
	err := s.DB.QueryRowContext(ctx, fmt.Sprintf(`SELECT id FROM %s WHERE name = ?`, t.table()), name).Scan(&id)
	return id, err
}

// DeleteGroup removes a set; its drives are no longer in the taxonomy.
func (s *Store) DeleteGroup(ctx context.Context, t Taxonomy, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE drives SET %s = NULL WHERE %s = ?`, t.column(), t.column()), id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, t.table()), id); err != nil {
		return err
	}
	return tx.Commit()
}

// RenameGroup renames a set.
func (s *Store) RenameGroup(ctx context.Context, t Taxonomy, id int64, name string) error {
	_, err := s.DB.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET name = ? WHERE id = ?`, t.table()), strings.TrimSpace(name), id)
	return err
}
