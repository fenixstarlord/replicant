-- Drives can be renamed. name is the catalog name shown everywhere;
-- volume_name is the name the volume itself reported on its latest scan;
-- renamed says the user chose name, so scans no longer overwrite it.
ALTER TABLE drives ADD COLUMN volume_name TEXT NOT NULL DEFAULT '';
ALTER TABLE drives ADD COLUMN renamed INTEGER NOT NULL DEFAULT 0;
UPDATE drives SET volume_name = name;
