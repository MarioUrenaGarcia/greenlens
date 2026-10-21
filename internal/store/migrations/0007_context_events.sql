-- Changes of what the measurements depend on: the version of the agent,
-- the kernel, the GPU driver, the backends, the files of the models and the
-- configuration. A regression of the energy per token is shown next to the
-- changes around it.
CREATE TABLE context_events (
  id INTEGER PRIMARY KEY,
  ts_ms INTEGER NOT NULL,
  kind TEXT NOT NULL,
  subject TEXT NOT NULL,
  value TEXT NOT NULL,
  previous TEXT NOT NULL
);
CREATE INDEX context_events_ts ON context_events (ts_ms);
CREATE INDEX context_events_subject ON context_events (kind, subject, id);
