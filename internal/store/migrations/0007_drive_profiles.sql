-- Profiles can be saved before a drive's first successful scan. Identity uses
-- exactly the same key as drives.volume_uuid, including LTFS UUIDs.
CREATE TABLE drive_profiles (
    volume_key TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    volume_name TEXT NOT NULL DEFAULT '',
    group_id INTEGER REFERENCES drive_groups(id) ON DELETE SET NULL,
    client_id INTEGER REFERENCES clients(id) ON DELETE SET NULL,
    fingerprint INTEGER NOT NULL DEFAULT 1,
    extractors_json TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1
);
