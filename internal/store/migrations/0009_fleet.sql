-- Energy of each node of a fleet, hour by hour and domain by domain, kept
-- by the hub that reads the agents.
CREATE TABLE fleet_energy (
    node TEXT NOT NULL,
    hour_ms INTEGER NOT NULL,
    domain TEXT NOT NULL,
    energy_j REAL NOT NULL,
    PRIMARY KEY (node, hour_ms, domain)
) WITHOUT ROWID;
CREATE INDEX fleet_energy_hour ON fleet_energy (hour_ms);
