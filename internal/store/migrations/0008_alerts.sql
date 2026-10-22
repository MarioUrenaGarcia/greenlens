-- Alerts keep when they fired and the comparison of their rule at the
-- time, so the history reads the same after the rule changes.
ALTER TABLE alert_events ADD COLUMN fired_ms INTEGER;
ALTER TABLE alert_events ADD COLUMN detail TEXT NOT NULL DEFAULT '{}';
CREATE INDEX alert_events_started ON alert_events (started_ms);
CREATE INDEX alert_events_state ON alert_events (state);
