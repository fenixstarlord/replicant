package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fenixstarlord/replicant/internal/scanprofile"
)

var ErrProfileConflict = errors.New("drive settings changed on another client; reload and try again")

// ProfileUpdate saves a complete profile, optionally creating taxonomy entries.
type ProfileUpdate struct {
	scanprofile.Profile
	NewGroup  string `json:"new_group,omitempty"`
	NewClient string `json:"new_client,omitempty"`
}

const profileSelect = `WITH keys AS (
	SELECT volume_uuid AS key FROM drives UNION SELECT volume_key AS key FROM drive_profiles
) SELECT k.key,
	CASE WHEN d.id IS NOT NULL THEN d.name ELSE p.name END,
	CASE WHEN d.id IS NOT NULL THEN d.volume_name ELSE p.volume_name END,
	CASE WHEN d.id IS NOT NULL THEN coalesce(d.group_id,0) ELSE coalesce(p.group_id,0) END,
	CASE WHEN d.id IS NOT NULL THEN coalesce(d.client_id,0) ELSE coalesce(p.client_id,0) END,
	coalesce(p.fingerprint,1), p.extractors_json, p.volume_key IS NOT NULL,
	EXISTS(SELECT 1 FROM scans WHERE drive_id = d.id), coalesce(p.revision,0)
	FROM keys k LEFT JOIN drives d ON d.volume_uuid = k.key
	LEFT JOIN drive_profiles p ON p.volume_key = k.key`

func (s *Store) ListDriveProfiles(ctx context.Context) ([]scanprofile.Profile, error) {
	rows, err := s.DB.QueryContext(ctx, profileSelect+` ORDER BY k.key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []scanprofile.Profile{}
	for rows.Next() {
		p, err := readProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func readProfile(row interface{ Scan(...any) error }) (scanprofile.Profile, error) {
	p := scanprofile.Profile{Settings: scanprofile.Default()}
	var raw sql.NullString
	err := row.Scan(&p.Key, &p.Name, &p.VolumeName, &p.GroupID, &p.ClientID,
		&p.Fingerprint, &raw, &p.Configured, &p.Indexed, &p.Revision)
	if err != nil {
		return p, err
	}
	if raw.Valid {
		if err := json.Unmarshal([]byte(raw.String), &p.Extractors); err != nil {
			return p, err
		}
	}
	return p, nil
}

func (s *Store) DriveProfile(ctx context.Context, key string) (scanprofile.Profile, error) {
	p, err := readProfile(s.DB.QueryRowContext(ctx, profileSelect+` WHERE k.key = ?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return scanprofile.Profile{Key: key, Settings: scanprofile.Default()}, nil
	}
	return p, err
}

func (u ProfileUpdate) Validate() error {
	if strings.TrimSpace(u.Key) == "" || len(u.Key) > 1024 {
		return errors.New("volume key is required (maximum 1024 bytes)")
	}
	if len(u.Name) > 512 || len(u.VolumeName) > 512 || len(u.NewGroup) > 512 || len(u.NewClient) > 512 {
		return errors.New("names must be at most 512 bytes")
	}
	if u.GroupID < 0 || u.ClientID < 0 || u.Revision < 0 {
		return errors.New("invalid profile identifiers")
	}
	return u.Settings.Validate()
}

func (s *Store) SaveDriveProfile(ctx context.Context, u ProfileUpdate) (scanprofile.Profile, error) {
	if err := u.Validate(); err != nil {
		return scanprofile.Profile{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return scanprofile.Profile{}, err
	}
	defer tx.Rollback()
	// Write first, acquiring SQLite's write lock before reading taxonomy IDs.
	raw, err := json.Marshal(append([]string{}, u.Extractors...))
	if err != nil {
		return scanprofile.Profile{}, err
	}
	var result sql.Result
	if u.Revision == 0 {
		result, err = tx.ExecContext(ctx, `INSERT INTO drive_profiles(volume_key,name,volume_name,fingerprint,extractors_json)
			VALUES (?,?,?,?,?) ON CONFLICT(volume_key) DO NOTHING`, u.Key, strings.TrimSpace(u.Name), u.VolumeName, u.Fingerprint, string(raw))
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE drive_profiles SET name=?,volume_name=?,fingerprint=?,extractors_json=?,revision=revision+1 WHERE volume_key=? AND revision=?`,
			strings.TrimSpace(u.Name), u.VolumeName, u.Fingerprint, string(raw), u.Key, u.Revision)
	}
	if err != nil {
		return scanprofile.Profile{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return scanprofile.Profile{}, err
	}
	if n == 0 {
		return scanprofile.Profile{}, ErrProfileConflict
	}
	group, err := profileTaxonomy(ctx, tx, ByGroup, u.GroupID, u.NewGroup)
	if err != nil {
		return scanprofile.Profile{}, err
	}
	client, err := profileTaxonomy(ctx, tx, ByClient, u.ClientID, u.NewClient)
	if err != nil {
		return scanprofile.Profile{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE drive_profiles SET group_id=nullif(?,0),client_id=nullif(?,0) WHERE volume_key=?`, group, client, u.Key); err != nil {
		return scanprofile.Profile{}, err
	}
	name := strings.TrimSpace(u.Name)
	if _, err := tx.ExecContext(ctx, `UPDATE drives SET name=CASE WHEN ?='' THEN volume_name ELSE ? END,
		renamed=(?!='' AND ?!=volume_name),group_id=nullif(?,0),client_id=nullif(?,0) WHERE volume_uuid=?`, name, name, name, name, group, client, u.Key); err != nil {
		return scanprofile.Profile{}, err
	}
	if err := tx.Commit(); err != nil {
		return scanprofile.Profile{}, err
	}
	return s.DriveProfile(ctx, u.Key)
}

func profileTaxonomy(ctx context.Context, tx *sql.Tx, t Taxonomy, id int64, name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name != "" {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s(name) VALUES (?) ON CONFLICT(name) DO NOTHING`, t.table()), name); err != nil {
			return 0, err
		}
		err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT id FROM %s WHERE name=?`, t.table()), name).Scan(&id)
		return id, err
	}
	if id == 0 {
		return 0, nil
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s WHERE id=?)`, t.table()), id).Scan(&exists); err != nil {
		return 0, err
	}
	if !exists {
		return 0, fmt.Errorf("no such %s", t)
	}
	return id, nil
}
