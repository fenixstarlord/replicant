package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestFTS5TrigramInMemory(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := CheckFTS5Trigram(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}

func TestFTS5TrigramSubstringAndCase(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE VIRTUAL TABLE f USING fts5(path, tokenize='trigram')`); err != nil {
		t.Fatal(err)
	}
	rows := []string{
		"A001/A001C003_240101_R1ZX.mxf",
		"B002/B002C010_240102_R1ZX.mxf",
		"AUDIO/SC12_T03.wav",
	}
	for _, r := range rows {
		if _, err := db.ExecContext(ctx, `INSERT INTO f(path) VALUES (?)`, r); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		q    string
		want int
	}{
		{"c003", 1},   // case-insensitive substring
		{"R1ZX", 2},   // shared substring across rows
		{"sc12_t", 1}, // underscore inside the trigram stream
		{"zzz", 0},
	}
	for _, c := range cases {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM f WHERE path MATCH ?`, c.q).Scan(&n); err != nil {
			t.Fatalf("%q: %v", c.q, err)
		}
		if n != c.want {
			t.Errorf("MATCH %q = %d rows, want %d", c.q, n, c.want)
		}
	}
	var mode string
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
}
