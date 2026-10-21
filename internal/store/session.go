// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/auth"
)

// SaveSession stores a new session.
func (s *Store) SaveSession(ctx context.Context, sess auth.Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (id_hash, role, actor, csrf, created_ms, last_seen_ms) VALUES (?, ?, ?, ?, ?, ?)`,
		sess.IDHash, string(sess.Role), sess.Actor, sess.CSRF, sess.CreatedAt.UnixMilli(), sess.LastSeen.UnixMilli())
	return err
}

// Session returns a stored session.
func (s *Store) Session(ctx context.Context, idHash string) (auth.Session, bool, error) {
	var sess auth.Session
	var role string
	var created, seen int64
	err := s.db.QueryRowContext(ctx, `SELECT id_hash, role, actor, csrf, created_ms, last_seen_ms FROM sessions WHERE id_hash = ?`, idHash).
		Scan(&sess.IDHash, &role, &sess.Actor, &sess.CSRF, &created, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Session{}, false, nil
	}
	if err != nil {
		return auth.Session{}, false, err
	}
	sess.Role, sess.CreatedAt, sess.LastSeen = auth.Role(role), time.UnixMilli(created), time.UnixMilli(seen)
	return sess, true, nil
}

// TouchSession records the last use of a session.
func (s *Store) TouchSession(ctx context.Context, idHash string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_ms = ? WHERE id_hash = ?`, at.UnixMilli(), idHash)
	return err
}

// DeleteSession ends a session.
func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, idHash)
	return err
}

// DeleteSessionsBefore removes the sessions unused since lastSeen or
// created before created.
func (s *Store) DeleteSessionsBefore(ctx context.Context, lastSeen, created time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE last_seen_ms < ? OR created_ms < ?`, lastSeen.UnixMilli(), created.UnixMilli())
	return err
}

// AuditEntry is one action of an operator or administrator.
type AuditEntry struct {
	ID     int64     `json:"id"`
	Time   time.Time `json:"time"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Detail string    `json:"detail"`
}

// AuditLog returns the latest entries, newest first, older than the entry
// before when it is positive.
func (s *Store) AuditLog(ctx context.Context, limit int, before int64) ([]AuditEntry, error) {
	q := `SELECT id, ts_ms, actor, action, detail FROM audit_log`
	args := []any{}
	if before > 0 {
		q += ` WHERE id < ?`
		args = append(args, before)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var ts int64
		if err := rows.Scan(&e.ID, &ts, &e.Actor, &e.Action, &e.Detail); err != nil {
			return nil, err
		}
		e.Time = time.UnixMilli(ts).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}
