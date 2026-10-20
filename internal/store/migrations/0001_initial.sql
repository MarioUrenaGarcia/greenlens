CREATE TABLE sources (
  id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, kind TEXT NOT NULL,
  domain TEXT NOT NULL, quality TEXT NOT NULL, first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL
);

CREATE TABLE node_samples (
  ts_ms INTEGER NOT NULL, interval_ms INTEGER NOT NULL, source_id INTEGER NOT NULL REFERENCES sources(id),
  energy_j REAL NOT NULL, power_w REAL NOT NULL,
  PRIMARY KEY (ts_ms, source_id)
);

CREATE TABLE backends (
  id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, kind TEXT NOT NULL, endpoint TEXT,
  first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL
);

CREATE TABLE models (
  id INTEGER PRIMARY KEY, backend_id INTEGER NOT NULL REFERENCES backends(id), name TEXT NOT NULL,
  first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL, UNIQUE (backend_id, name)
);

CREATE TABLE consumers (
  id INTEGER PRIMARY KEY, alias TEXT NOT NULL UNIQUE, key_hmac TEXT UNIQUE, created_at INTEGER NOT NULL
);

CREATE TABLE usage_intervals (
  ts_ms INTEGER NOT NULL, interval_ms INTEGER NOT NULL,
  backend_id INTEGER NOT NULL REFERENCES backends(id), model_id INTEGER REFERENCES models(id),
  consumer_id INTEGER REFERENCES consumers(id),
  cpu_energy_j REAL NOT NULL, dram_energy_j REAL NOT NULL, gpu_energy_j REAL NOT NULL,
  prompt_tokens INTEGER, generated_tokens INTEGER, requests INTEGER,
  method TEXT NOT NULL, quality TEXT NOT NULL,
  PRIMARY KEY (ts_ms, backend_id, model_id, consumer_id)
);

CREATE TABLE usage_rollups (
  bucket_start_ms INTEGER NOT NULL, resolution TEXT NOT NULL,
  backend_id INTEGER NOT NULL, model_id INTEGER, consumer_id INTEGER,
  energy_j REAL NOT NULL, prompt_tokens INTEGER, generated_tokens INTEGER, requests INTEGER,
  co2e_g REAL, cost REAL, water_l REAL,
  PRIMARY KEY (bucket_start_ms, resolution, backend_id, model_id, consumer_id)
);

CREATE TABLE calibrations (id INTEGER PRIMARY KEY, ts_ms INTEGER NOT NULL, duration_s INTEGER NOT NULL, results TEXT NOT NULL);

CREATE TABLE benchmarks (
  id INTEGER PRIMARY KEY, started_at_ms INTEGER NOT NULL, ended_at_ms INTEGER NOT NULL,
  backend_kind TEXT NOT NULL, model TEXT NOT NULL, prompt_set_sha256 TEXT NOT NULL,
  params TEXT NOT NULL, context TEXT NOT NULL, runs INTEGER NOT NULL,
  energy_j_mean REAL NOT NULL, energy_j_stddev REAL NOT NULL, generated_tokens INTEGER NOT NULL,
  j_per_token_mean REAL NOT NULL, results TEXT NOT NULL
);

CREATE TABLE alert_events (
  id INTEGER PRIMARY KEY, rule TEXT NOT NULL, state TEXT NOT NULL, value REAL NOT NULL,
  labels TEXT NOT NULL, started_ms INTEGER NOT NULL, resolved_ms INTEGER
);

CREATE TABLE audit_log (
  id INTEGER PRIMARY KEY, ts_ms INTEGER NOT NULL, actor TEXT NOT NULL, action TEXT NOT NULL, detail TEXT NOT NULL
);
