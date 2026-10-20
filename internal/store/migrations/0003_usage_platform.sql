-- Energy of a backend from a platform or wall meter, attributed under the
-- wall policy; zero under the other policies.
ALTER TABLE usage_intervals ADD COLUMN platform_energy_j REAL NOT NULL DEFAULT 0;
