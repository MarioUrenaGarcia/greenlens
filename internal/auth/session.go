// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// Session lifetimes: a session ends after a week without use, and after a
// month in any case.
const (
	SessionIdle     = 7 * 24 * time.Hour
	SessionLifetime = 30 * 24 * time.Hour
	// touchEvery bounds how often the last use is written.
	touchEvery = time.Minute
)

// Session is a signed-in browser. Only the SHA-256 of its identifier is
// stored, so a copy of the database cannot be used to sign in.
type Session struct {
	IDHash    string
	Role      Role
	Actor     string
	CSRF      string
	CreatedAt time.Time
	LastSeen  time.Time
}

// SessionStore keeps sessions across restarts of the agent.
type SessionStore interface {
	SaveSession(ctx context.Context, s Session) error
	Session(ctx context.Context, idHash string) (Session, bool, error)
	TouchSession(ctx context.Context, idHash string, at time.Time) error
	DeleteSession(ctx context.Context, idHash string) error
	DeleteSessionsBefore(ctx context.Context, lastSeen, created time.Time) error
}

// Sessions creates and checks sessions.
type Sessions struct {
	store SessionStore
	now   func() time.Time
}

// NewSessions returns sessions kept in store.
func NewSessions(store SessionStore) *Sessions {
	return &Sessions{store: store, now: time.Now}
}

// HashID returns the stored form of a session identifier.
func HashID(id string) string {
	h := sha256.Sum256([]byte(id))
	return hex.EncodeToString(h[:])
}

// Create starts a session and returns its identifier, which goes only into
// the cookie of the browser.
func (s *Sessions) Create(ctx context.Context, role Role, actor string) (string, Session, error) {
	id, err := randomText(32)
	if err != nil {
		return "", Session{}, err
	}
	csrf, err := randomText(32)
	if err != nil {
		return "", Session{}, err
	}
	now := s.now()
	sess := Session{IDHash: HashID(id), Role: role, Actor: actor, CSRF: csrf, CreatedAt: now, LastSeen: now}
	if err := s.store.SaveSession(ctx, sess); err != nil {
		return "", Session{}, err
	}
	return id, sess, nil
}

// Lookup returns the session of an identifier that is still valid.
func (s *Sessions) Lookup(ctx context.Context, id string) (Session, bool, error) {
	if len(id) != 43 {
		return Session{}, false, nil
	}
	sess, ok, err := s.store.Session(ctx, HashID(id))
	if err != nil || !ok {
		return Session{}, false, err
	}
	now := s.now()
	if now.Sub(sess.LastSeen) > SessionIdle || now.Sub(sess.CreatedAt) > SessionLifetime || !sess.Role.Valid() {
		return Session{}, false, s.store.DeleteSession(ctx, sess.IDHash)
	}
	if now.Sub(sess.LastSeen) >= touchEvery {
		if err := s.store.TouchSession(ctx, sess.IDHash, now); err != nil {
			return Session{}, false, err
		}
		sess.LastSeen = now
	}
	return sess, true, nil
}

// End deletes a session.
func (s *Sessions) End(ctx context.Context, id string) error {
	return s.store.DeleteSession(ctx, HashID(id))
}

// Prune deletes the sessions that can no longer be used.
func (s *Sessions) Prune(ctx context.Context) error {
	now := s.now()
	return s.store.DeleteSessionsBefore(ctx, now.Add(-SessionIdle), now.Add(-SessionLifetime))
}

// CodeLifetime is how long a sign-in code can be used.
const CodeLifetime = 2 * time.Minute

// maxCodes bounds the codes waiting to be used.
const maxCodes = 64

type code struct {
	role    Role
	actor   string
	expires time.Time
}

// Codes are one-time sign-in codes handed out to local users, so that a
// browser opened by greenlens gui starts a session without typing a token.
type Codes struct {
	now func() time.Time

	mu    sync.Mutex
	codes map[string]code
}

// NewCodes returns an empty set of codes.
func NewCodes() *Codes {
	return &Codes{now: time.Now, codes: map[string]code{}}
}

// Issue returns a new code for role and actor.
func (c *Codes) Issue(role Role, actor string) (string, error) {
	v, err := randomText(32)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for k, x := range c.codes {
		if !now.Before(x.expires) {
			delete(c.codes, k)
		}
	}
	if len(c.codes) >= maxCodes {
		// The oldest code makes room; codes are short-lived anyway.
		var oldest string
		for k, x := range c.codes {
			if oldest == "" || x.expires.Before(c.codes[oldest].expires) {
				oldest = k
			}
		}
		delete(c.codes, oldest)
	}
	c.codes[HashID(v)] = code{role: role, actor: actor, expires: now.Add(CodeLifetime)}
	return v, nil
}

// Redeem consumes a code. A code works once and only before it expires.
func (c *Codes) Redeem(v string) (Role, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := HashID(v)
	x, ok := c.codes[k]
	if !ok {
		return "", "", false
	}
	delete(c.codes, k)
	if !c.now().Before(x.expires) {
		return "", "", false
	}
	return x.role, x.actor, true
}
