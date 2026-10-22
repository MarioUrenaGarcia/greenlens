// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/alerts"
)

// alertDetail is the comparison of the rule of an alert when it started.
type alertDetail struct {
	Expr      string  `json:"expr"`
	Op        string  `json:"op"`
	Threshold float64 `json:"threshold"`
	ForS      float64 `json:"for_s"`
}

func msOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixMilli()
}

// InsertAlert stores a new alert and returns its row.
func (s *Store) InsertAlert(ctx context.Context, a alerts.Alert) (int64, error) {
	labels, err := json.Marshal(a.Labels)
	if err != nil {
		return 0, err
	}
	detail, err := json.Marshal(alertDetail{a.Expr, a.Op, a.Threshold, a.ForS})
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, `INSERT INTO alert_events (rule, state, value, labels, started_ms, fired_ms, resolved_ms, detail)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, a.Rule, a.State, a.Value, string(labels), a.StartedAt.UnixMilli(), msOrNil(a.FiredAt), msOrNil(a.ResolvedAt), string(detail))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateAlert stores the new state of an alert.
func (s *Store) UpdateAlert(ctx context.Context, a alerts.Alert) error {
	detail, err := json.Marshal(alertDetail{a.Expr, a.Op, a.Threshold, a.ForS})
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.ExecContext(ctx, `UPDATE alert_events SET state = ?, value = ?, fired_ms = ?, resolved_ms = ?, detail = ? WHERE id = ?`,
		a.State, a.Value, msOrNil(a.FiredAt), msOrNil(a.ResolvedAt), string(detail), a.ID)
	return err
}

// DeleteAlert removes an alert, as one that ended before it fired.
func (s *Store) DeleteAlert(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM alert_events WHERE id = ?`, id)
	return err
}

const alertColumns = `id, rule, state, value, labels, started_ms, fired_ms, resolved_ms, detail`

func scanAlerts(rows *sql.Rows) ([]alerts.Alert, error) {
	defer func() { _ = rows.Close() }()
	out := []alerts.Alert{}
	for rows.Next() {
		var a alerts.Alert
		var labels, detail string
		var started int64
		var fired, resolved sql.NullInt64
		if err := rows.Scan(&a.ID, &a.Rule, &a.State, &a.Value, &labels, &started, &fired, &resolved, &detail); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(labels), &a.Labels); err != nil {
			return nil, err
		}
		var d alertDetail
		if err := json.Unmarshal([]byte(detail), &d); err != nil {
			return nil, err
		}
		a.Expr, a.Op, a.Threshold, a.ForS = d.Expr, d.Op, d.Threshold, d.ForS
		a.StartedAt = time.UnixMilli(started).UTC()
		if fired.Valid {
			t := time.UnixMilli(fired.Int64).UTC()
			a.FiredAt = &t
		}
		if resolved.Valid {
			t := time.UnixMilli(resolved.Int64).UTC()
			a.ResolvedAt = &t
		}
		if a.Labels == nil {
			a.Labels = map[string]string{}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ActiveAlerts returns the alerts that are pending or firing.
func (s *Store) ActiveAlerts(ctx context.Context) ([]alerts.Alert, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+alertColumns+` FROM alert_events WHERE state IN ('pending', 'firing') ORDER BY started_ms`)
	if err != nil {
		return nil, err
	}
	return scanAlerts(rows)
}

// AlertHistory returns the alerts that fired and started in [from, to),
// the latest first, at most limit of them.
func (s *Store) AlertHistory(ctx context.Context, from, to time.Time, limit int) ([]alerts.Alert, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+alertColumns+` FROM alert_events
		WHERE fired_ms IS NOT NULL AND started_ms >= ? AND started_ms < ? ORDER BY started_ms DESC, id DESC LIMIT ?`,
		from.UnixMilli(), to.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	return scanAlerts(rows)
}
