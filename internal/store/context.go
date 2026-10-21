// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package store

import (
	"context"
	"time"
)

// Kinds of context events.
const (
	ContextAgentVersion   = "agent_version"
	ContextKernel         = "kernel"
	ContextGPUDriver      = "gpu_driver"
	ContextBackendVersion = "backend_version"
	ContextModelDigest    = "model_digest"
	ContextConfig         = "config"
)

// ContextEvent is a change of what the measurements depend on. The first
// value seen of a subject has no previous one.
type ContextEvent struct {
	ID       int64     `json:"id"`
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	Subject  string    `json:"subject,omitempty"`
	Value    string    `json:"value"`
	Previous string    `json:"previous,omitempty"`
}

// LatestContext returns the last value of every kind and subject, keyed by
// kind, a NUL and the subject.
func (s *Store) LatestContext(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, subject, value FROM context_events e
		WHERE id = (SELECT max(id) FROM context_events WHERE kind = e.kind AND subject = e.subject)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var kind, subject, value string
		if err := rows.Scan(&kind, &subject, &value); err != nil {
			return nil, err
		}
		out[ContextKey(kind, subject)] = value
	}
	return out, rows.Err()
}

// ContextKey joins a kind and a subject as LatestContext does.
func ContextKey(kind, subject string) string { return kind + "\x00" + subject }

// RecordContext stores a value of a subject and the one it replaces.
func (s *Store) RecordContext(ctx context.Context, at time.Time, kind, subject, value, previous string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `INSERT INTO context_events (ts_ms, kind, subject, value, previous) VALUES (?, ?, ?, ?, ?)`,
		at.UnixMilli(), kind, subject, value, previous)
	return err
}

// ContextEvents returns the events in [from, to), oldest first.
func (s *Store) ContextEvents(ctx context.Context, from, to time.Time) ([]ContextEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, ts_ms, kind, subject, value, previous FROM context_events
		WHERE ts_ms >= ? AND ts_ms < ? ORDER BY ts_ms, id`, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []ContextEvent{}
	for rows.Next() {
		var e ContextEvent
		var ms int64
		if err := rows.Scan(&e.ID, &ms, &e.Kind, &e.Subject, &e.Value, &e.Previous); err != nil {
			return nil, err
		}
		e.At = time.UnixMilli(ms).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}
