-- Who scanned what, and what is being scanned right now.
ALTER TABLE scans ADD COLUMN host   TEXT NOT NULL DEFAULT '';  -- computer that ran the scan
ALTER TABLE scans ADD COLUMN source TEXT NOT NULL DEFAULT '';  -- API key name, 'server', 'file', or 'local'

CREATE TABLE activity (
    id           INTEGER PRIMARY KEY,
    host         TEXT NOT NULL DEFAULT '',
    source       TEXT NOT NULL DEFAULT '',
    drive_name   TEXT NOT NULL DEFAULT '',
    volume_uuid  TEXT NOT NULL DEFAULT '',
    root         TEXT NOT NULL DEFAULT '',
    stage        TEXT NOT NULL DEFAULT 'starting',   -- starting, walk, hash, extract, upload, ingest
    done         INTEGER NOT NULL DEFAULT 0,
    total        INTEGER NOT NULL DEFAULT 0,
    status       TEXT NOT NULL DEFAULT 'running',    -- running, done, error, cancelled, lost
    error        TEXT NOT NULL DEFAULT '',
    scan_id      INTEGER REFERENCES scans(id) ON DELETE SET NULL,
    started_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    finished_at  TEXT
);
CREATE INDEX activity_status ON activity(status, updated_at);
CREATE INDEX activity_started ON activity(started_at);

-- Scans stored before this table existed become finished history rows.
INSERT INTO activity (host, source, drive_name, volume_uuid, root, stage, status, scan_id, started_at, updated_at, finished_at)
SELECT s.host, s.source, d.name, d.volume_uuid, s.root, 'ingest', 'done', s.id, s.scanned_at, s.ingested_at, s.ingested_at
FROM scans s JOIN drives d ON d.id = s.drive_id;
