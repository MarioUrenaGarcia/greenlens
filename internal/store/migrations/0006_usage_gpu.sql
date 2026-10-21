-- The GPU part of the energy of each rollup, so the embodied carbon of a
-- GPU can be shared by GPU energy. Rows folded before this version count
-- their whole energy as processor and memory.
ALTER TABLE usage_rollups ADD COLUMN gpu_energy_j REAL NOT NULL DEFAULT 0;
