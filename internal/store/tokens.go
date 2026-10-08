package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"time"
)

// TokenPrefix marks API tokens so they are recognisable in logs and configs.
const TokenPrefix = "shelf_"

// Token is an API token row (never the secret itself).
type Token struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// CreateToken mints a new API token and stores only its hash. The plain
// token is returned once and cannot be recovered later.
func (s *Store) CreateToken(ctx context.Context, name string) (plain string, id int64, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", 0, err
	}
	plain = TokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO api_tokens(name, token_hash, created_at) VALUES (?, ?, ?)`,
		name, hashToken(plain), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return "", 0, err
	}
	id, err = res.LastInsertId()
	return plain, id, err
}

// VerifyToken looks up a plain token. It returns ok=false for unknown
// tokens and records last_used_at for known ones.
func (s *Store) VerifyToken(ctx context.Context, plain string) (tok Token, ok bool, err error) {
	var created string
	err = s.DB.QueryRowContext(ctx, `SELECT id, name, created_at FROM api_tokens WHERE token_hash = ?`,
		hashToken(plain)).Scan(&tok.ID, &tok.Name, &created)
	if err == sql.ErrNoRows {
		return Token{}, false, nil
	}
	if err != nil {
		return Token{}, false, err
	}
	tok.CreatedAt, _ = time.Parse(time.RFC3339, created)
	_, _ = s.DB.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), tok.ID)
	return tok, true, nil
}

// ListTokens returns all tokens, newest first.
func (s *Store) ListTokens(ctx context.Context) ([]Token, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, created_at, last_used_at FROM api_tokens ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		var t Token
		var created string
		var used sql.NullString
		if err := rows.Scan(&t.ID, &t.Name, &created, &used); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339, created)
		if used.Valid {
			if u, err := time.Parse(time.RFC3339, used.String); err == nil {
				t.LastUsedAt = &u
			}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeToken deletes a token by id. It reports whether one was deleted.
func (s *Store) RevokeToken(ctx context.Context, id int64) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
