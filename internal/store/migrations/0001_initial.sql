CREATE TABLE drives (
    id              INTEGER PRIMARY KEY,
    volume_uuid     TEXT NOT NULL UNIQUE,
    name            TEXT NOT NULL,
    label           TEXT NOT NULL DEFAULT '',
    location        TEXT NOT NULL DEFAULT '',
    notes           TEXT NOT NULL DEFAULT '',
    fs_type         TEXT NOT NULL DEFAULT '',
    capacity_bytes  INTEGER NOT NULL DEFAULT 0,
    free_bytes      INTEGER NOT NULL DEFAULT 0,
    device          TEXT NOT NULL DEFAULT '',
    media_name      TEXT NOT NULL DEFAULT '',
    protocol        TEXT NOT NULL DEFAULT '',
    first_seen      TEXT NOT NULL,
    last_seen       TEXT NOT NULL
);

CREATE TABLE scans (
    id               INTEGER PRIMARY KEY,
    drive_id         INTEGER NOT NULL REFERENCES drives(id),
    scanned_at       TEXT NOT NULL,
    ingested_at      TEXT NOT NULL,
    scanner_version  TEXT NOT NULL DEFAULT '',
    root             TEXT NOT NULL DEFAULT '',
    options_json     TEXT NOT NULL DEFAULT '{}',
    extractors_json  TEXT NOT NULL DEFAULT '[]',
    file_count       INTEGER NOT NULL DEFAULT 0,
    dir_count        INTEGER NOT NULL DEFAULT 0,
    clip_count       INTEGER NOT NULL DEFAULT 0,
    total_bytes      INTEGER NOT NULL DEFAULT 0,
    added            INTEGER NOT NULL DEFAULT 0,
    removed          INTEGER NOT NULL DEFAULT 0,
    changed          INTEGER NOT NULL DEFAULT 0,
    is_latest        INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX scans_drive ON scans(drive_id, scanned_at);

CREATE TABLE entries (
    id              INTEGER PRIMARY KEY,
    scan_id         INTEGER NOT NULL REFERENCES scans(id) ON DELETE CASCADE,
    parent_path     TEXT NOT NULL,
    path            TEXT NOT NULL,
    name            TEXT NOT NULL,
    ext             TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL,
    is_dir          INTEGER NOT NULL DEFAULT 0,
    is_package      INTEGER NOT NULL DEFAULT 0,
    is_symlink      INTEGER NOT NULL DEFAULT 0,
    size            INTEGER NOT NULL DEFAULT 0,
    mtime           TEXT,
    btime           TEXT,
    fingerprint     TEXT,
    full_hash       TEXT,
    mhl_hash        TEXT,
    clip_key        TEXT,
    error           TEXT,
    dir_total_size  INTEGER,
    dir_file_count  INTEGER,
    UNIQUE(scan_id, path)
);
CREATE INDEX entries_parent      ON entries(scan_id, parent_path);
CREATE INDEX entries_fingerprint ON entries(fingerprint) WHERE fingerprint IS NOT NULL;
CREATE INDEX entries_name_size   ON entries(name, size);
CREATE INDEX entries_clip        ON entries(scan_id, clip_key);

CREATE TABLE clips (
    id              INTEGER PRIMARY KEY,
    scan_id         INTEGER NOT NULL REFERENCES scans(id) ON DELETE CASCADE,
    clip_key        TEXT NOT NULL,
    root_path       TEXT NOT NULL,
    name            TEXT NOT NULL,
    kind            TEXT NOT NULL,
    file_count      INTEGER NOT NULL DEFAULT 0,
    total_size      INTEGER NOT NULL DEFAULT 0,
    mtime           TEXT,
    files_json      TEXT NOT NULL DEFAULT '[]',
    sidecars_json   TEXT NOT NULL DEFAULT '[]',
    seq_frame_count INTEGER,
    seq_first_frame INTEGER,
    seq_last_frame  INTEGER,
    -- normalized metadata, filled by extractors (Phase 4); all nullable
    clip_name       TEXT,
    reel            TEXT,
    camera_index    TEXT,
    scene           TEXT,
    take            TEXT,
    circled         INTEGER,
    recorded_at     TEXT,
    tc_start        TEXT,
    tc_end          TEXT,
    duration_s      REAL,
    frame_count     INTEGER,
    container       TEXT,
    codec           TEXT,
    codec_detail    TEXT,
    width           INTEGER,
    height          INTEGER,
    sensor_mode     TEXT,
    fps             REAL,
    capture_fps     REAL,
    bit_depth       INTEGER,
    color_gamma     TEXT,
    squeeze         REAL,
    camera_make     TEXT,
    camera_model    TEXT,
    camera_serial   TEXT,
    firmware        TEXT,
    iso             INTEGER,
    wb_kelvin       INTEGER,
    tint            REAL,
    shutter_angle   REAL,
    shutter_speed   TEXT,
    nd              TEXT,
    lens            TEXT,
    focal_mm        REAL,
    t_stop          REAL,
    focus_distance  TEXT,
    audio_channels  INTEGER,
    sample_rate     INTEGER,
    audio_bit_depth INTEGER,
    look            TEXT,
    field_sources_json TEXT NOT NULL DEFAULT '{}',
    errors_json     TEXT NOT NULL DEFAULT '[]',
    UNIQUE(scan_id, clip_key)
);
CREATE INDEX clips_scan ON clips(scan_id, root_path);

CREATE TABLE clip_raw (
    clip_id   INTEGER NOT NULL REFERENCES clips(id) ON DELETE CASCADE,
    source    TEXT NOT NULL,
    raw_json  TEXT NOT NULL,
    PRIMARY KEY (clip_id, source)
);

CREATE VIRTUAL TABLE clips_fts USING fts5(
    clip_id UNINDEXED, scan_id UNINDEXED,
    name, reel, scene, take, camera, lens, look, path,
    tokenize = 'trigram'
);
-- Names only, as an external-content index over entries. Paths are not
-- trigram-indexed: every path repeats its parent prefix, which made the
-- index take ~35 s and ~2 GB per 600k-entry scan. A path substring search
-- matches directory names here, and queries containing '/' fall back to
-- LIKE on entries.path.
CREATE VIRTUAL TABLE entries_fts USING fts5(
    name,
    content = 'entries', content_rowid = 'id',
    tokenize = 'trigram'
);

CREATE TABLE scan_changes (
    scan_id   INTEGER NOT NULL REFERENCES scans(id) ON DELETE CASCADE,
    path      TEXT NOT NULL,
    change    TEXT NOT NULL,   -- added | removed | changed
    old_size  INTEGER,
    new_size  INTEGER,
    PRIMARY KEY (scan_id, path)
);

CREATE TABLE api_tokens (
    id           INTEGER PRIMARY KEY,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    created_at   TEXT NOT NULL,
    last_used_at TEXT
);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
