-- Node energy kept beyond the raw retention, per channel, at one-minute and
-- one-hour resolution.
CREATE TABLE node_rollups (
  bucket_start_ms INTEGER NOT NULL, resolution TEXT NOT NULL, source_id INTEGER NOT NULL REFERENCES sources(id),
  energy_j REAL NOT NULL, samples INTEGER NOT NULL,
  PRIMARY KEY (bucket_start_ms, resolution, source_id)
);

-- End of the period already folded into each rollup resolution, so the
-- agent resumes where it stopped after a restart.
CREATE TABLE rollup_state (
  resolution TEXT PRIMARY KEY, done_until_ms INTEGER NOT NULL
);
