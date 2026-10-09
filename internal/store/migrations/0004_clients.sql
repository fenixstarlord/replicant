-- A second, independent way to organise drives: by client.
CREATE TABLE clients (
    id    INTEGER PRIMARY KEY,
    name  TEXT NOT NULL UNIQUE
);
ALTER TABLE drives ADD COLUMN client_id INTEGER REFERENCES clients(id) ON DELETE SET NULL;
