-- Browser sessions of the HTTP API. Only the SHA-256 of the session
-- identifier is stored, so a copy of the database cannot sign anyone in.
CREATE TABLE sessions (
  id_hash TEXT PRIMARY KEY,
  role TEXT NOT NULL,
  actor TEXT NOT NULL,
  csrf TEXT NOT NULL,
  created_ms INTEGER NOT NULL,
  last_seen_ms INTEGER NOT NULL
);
CREATE INDEX sessions_last_seen ON sessions (last_seen_ms);
CREATE INDEX audit_log_ts ON audit_log (ts_ms);
