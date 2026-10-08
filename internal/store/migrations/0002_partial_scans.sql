-- A scan whose root is not the volume's mount point is partial. Latest
-- and diffs are tracked per (drive, root); drive aggregates and default
-- browsing prefer the latest full scan.
ALTER TABLE scans ADD COLUMN is_partial INTEGER NOT NULL DEFAULT 0;
CREATE INDEX scans_drive_root ON scans(drive_id, root, is_latest);
