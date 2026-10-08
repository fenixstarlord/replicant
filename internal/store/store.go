// Package store owns the SQLite database: opening, migrations, and queries.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Open opens (creating if needed) the SQLite database at path with WAL
// journaling, a busy timeout, and foreign keys enabled. Use ":memory:" for
// an in-memory database.
func Open(path string) (*sql.DB, error) {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(ON)")
	if path != ":memory:" {
		q.Add("_pragma", "journal_mode(WAL)")
	}
	dsn := "file:" + path + "?" + q.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if path == ":memory:" {
		// Each pooled connection would otherwise get its own empty database.
		db.SetMaxOpenConns(1)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// CheckFTS5Trigram verifies that the SQLite build supports FTS5 with the
// trigram tokenizer, which the catalog relies on for substring search.
// It creates and drops a temporary table.
func CheckFTS5Trigram(ctx context.Context, db *sql.DB) error {
	var version string
	if err := db.QueryRowContext(ctx, `SELECT sqlite_version()`).Scan(&version); err != nil {
		return fmt.Errorf("sqlite version: %w", err)
	}
	stmts := []string{
		`CREATE VIRTUAL TABLE temp.fts_check USING fts5(name, tokenize='trigram')`,
		`INSERT INTO temp.fts_check(name) VALUES ('A001C003_240101_R1ZX.mxf')`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("fts5 trigram unsupported (sqlite %s): %w", version, err)
		}
	}
	defer db.ExecContext(ctx, `DROP TABLE temp.fts_check`) //nolint:errcheck
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM temp.fts_check WHERE name MATCH 'c003'`).Scan(&n); err != nil {
		return fmt.Errorf("fts5 trigram query (sqlite %s): %w", version, err)
	}
	if n != 1 {
		return fmt.Errorf("fts5 trigram substring match returned %d rows, want 1 (sqlite %s)", n, version)
	}
	return nil
}
