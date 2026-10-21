// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package auth holds the access rules of the HTTP API: roles, access
// tokens stored as argon2id hashes, browser sessions and the one-time codes
// that open a session from the command line.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// Role is what a principal may do. Each role includes the ones below it.
type Role string

// Roles, from the least to the most privileged.
const (
	// Viewer reads measurements, reports and the state of the node.
	Viewer Role = "viewer"
	// Operator also runs benchmarks and calibrations.
	Operator Role = "operator"
	// Admin also manages tokens, the configuration and backups.
	Admin Role = "admin"
)

// Roles lists every role, from the least to the most privileged.
var Roles = []Role{Viewer, Operator, Admin}

func (r Role) rank() int {
	for i, x := range Roles {
		if x == r {
			return i + 1
		}
	}
	return 0
}

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r.rank() > 0 }

// Allows reports whether r may do what need requires.
func (r Role) Allows(need Role) bool { return need.Valid() && r.rank() >= need.rank() }

// ParseRole accepts the name of a role.
func ParseRole(s string) (Role, error) {
	r := Role(s)
	if !r.Valid() {
		return "", fmt.Errorf("unknown role %q; valid: viewer, operator, admin", s)
	}
	return r, nil
}

// Principal is who sends a request.
type Principal struct {
	Role Role
	// Actor names the principal in the audit log, such as uid:1000 or
	// token:grafana. It never contains a secret.
	Actor string
	// Via is how the principal was recognized: socket, session or token.
	Via string
	// CSRF is the token a session must echo on requests that change
	// something.
	CSRF string
}

// Ways a principal is recognized.
const (
	ViaSocket  = "socket"
	ViaSession = "session"
	ViaToken   = "token"
)

type principalKey struct{}

// WithPrincipal stores p in ctx.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// FromContext returns the principal of a request, if any.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// randomText returns n random bytes encoded as unpadded base64url.
func randomText(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
