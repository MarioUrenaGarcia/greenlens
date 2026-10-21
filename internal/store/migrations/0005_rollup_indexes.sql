-- Queries read one resolution over a range of times. The primary keys start
-- with the time, so a range of hourly rows would also walk every minute row
-- between them; these indexes start with the resolution.
CREATE INDEX node_rollups_resolution ON node_rollups (resolution, bucket_start_ms);
CREATE INDEX usage_rollups_resolution ON usage_rollups (resolution, bucket_start_ms);
