// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package store

import (
	"context"
	"time"
)

// FleetEnergy is the energy of one domain of a node over one hour.
type FleetEnergy struct {
	Node    string    `json:"node"`
	Hour    time.Time `json:"hour"`
	Domain  string    `json:"domain"`
	EnergyJ float64   `json:"energy_j"`
}

// AddFleetEnergy adds energy to the hour that contains at, for each
// domain of a node. Domains without energy are left out.
func (s *Store) AddFleetEnergy(ctx context.Context, node string, at time.Time, domains map[string]float64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	hour := at.UTC().Truncate(time.Hour).UnixMilli()
	for domain, j := range domains {
		if j <= 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO fleet_energy (node, hour_ms, domain, energy_j) VALUES (?, ?, ?, ?)
			ON CONFLICT (node, hour_ms, domain) DO UPDATE SET energy_j = energy_j + excluded.energy_j`, node, hour, domain, j); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// FleetEnergyBetween returns the hours that start in [from, to), ordered
// by hour, node and domain.
func (s *Store) FleetEnergyBetween(ctx context.Context, from, to time.Time) ([]FleetEnergy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT node, hour_ms, domain, energy_j FROM fleet_energy
		WHERE hour_ms >= ? AND hour_ms < ? ORDER BY hour_ms, node, domain`, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []FleetEnergy{}
	for rows.Next() {
		var e FleetEnergy
		var ms int64
		if err := rows.Scan(&e.Node, &ms, &e.Domain, &e.EnergyJ); err != nil {
			return nil, err
		}
		e.Hour = time.UnixMilli(ms).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// PruneFleetEnergy deletes the hours before a time and returns how many
// rows it deleted.
func (s *Store) PruneFleetEnergy(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM fleet_energy WHERE hour_ms < ?`, before.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
