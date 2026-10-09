-- Drive groups (user-defined shelves/sets) and server-side scan jobs.
CREATE TABLE drive_groups (
    id    INTEGER PRIMARY KEY,
    name  TEXT NOT NULL UNIQUE
);
ALTER TABLE drives ADD COLUMN group_id INTEGER REFERENCES drive_groups(id) ON DELETE SET NULL;

CREATE TABLE scan_jobs (
    id            INTEGER PRIMARY KEY,
    path          TEXT NOT NULL UNIQUE,
    label         TEXT NOT NULL DEFAULT '',
    interval_min  INTEGER NOT NULL DEFAULT 0,   -- 0 = manual only
    extract       INTEGER NOT NULL DEFAULT 1,
    fingerprint   INTEGER NOT NULL DEFAULT 1,
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    TEXT NOT NULL,
    last_run_at   TEXT,
    last_status   TEXT NOT NULL DEFAULT '',     -- '', running, ok, error
    last_error    TEXT NOT NULL DEFAULT '',
    last_scan_id  INTEGER,
    last_duration_ms INTEGER NOT NULL DEFAULT 0,
    next_run_at   TEXT
);
