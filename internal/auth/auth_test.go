// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package auth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRoles(t *testing.T) {
	for _, tt := range []struct {
		have, need Role
		want       bool
	}{
		{Admin, Viewer, true}, {Admin, Admin, true}, {Operator, Viewer, true}, {Operator, Admin, false},
		{Viewer, Operator, false}, {Viewer, Viewer, true}, {"", Viewer, false}, {Admin, "", false},
	} {
		if got := tt.have.Allows(tt.need); got != tt.want {
			t.Errorf("%q allows %q = %v", tt.have, tt.need, got)
		}
	}
	if _, err := ParseRole("root"); err == nil {
		t.Error("unknown role accepted")
	}
}

func TestTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "tokens")
	ts, err := OpenTokens(path)
	if err != nil || ts.Len() != 0 {
		t.Fatalf("empty store: %v %d", err, ts.Len())
	}
	secret, tk, err := ts.Create("grafana", Viewer, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, TokenPrefix+tk.ID+"_") || tk.Hash != "" || tk.ExpiresAt != nil {
		t.Fatalf("secret %q token %+v", secret, tk)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v %v", st, err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), secret[len(TokenPrefix)+13:]) || !strings.Contains(string(data), "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("the file keeps the secret or lacks the hash:\n%s", data)
	}
	if _, _, err := ts.Create("grafana", Admin, 0); err == nil {
		t.Error("a second token with the same name was created")
	}
	for _, bad := range []string{"Grafana", "", "a b", strings.Repeat("x", 64)} {
		if _, _, err := ts.Create(bad, Viewer, 0); err == nil {
			t.Errorf("name %q accepted", bad)
		}
	}

	got, ok := ts.Verify(secret)
	if !ok || got.Name != "grafana" || got.Role != Viewer || got.Hash != "" {
		t.Fatalf("verify: %+v %v", got, ok)
	}
	// The cached verification still compares the whole secret.
	if _, ok := ts.Verify(secret[:len(secret)-1] + "A"); ok && !strings.HasSuffix(secret, "A") {
		t.Fatal("a changed secret was accepted")
	}
	for _, bad := range []string{"", "greenlens_", secret + "x", strings.Replace(secret, tk.ID, "000000000000", 1), "Bearer " + secret} {
		if _, ok := ts.Verify(bad); ok {
			t.Errorf("verify accepted %q", bad)
		}
	}

	// Another process, such as greenlens token run while the agent is
	// stopped, sees the same file.
	other, err := OpenTokens(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := other.Verify(secret); !ok {
		t.Fatal("a second reader rejects the token")
	}
	if list := other.List(); len(list) != 1 || list[0].Hash != "" {
		t.Fatalf("list %+v", list)
	}
	if _, err := other.Revoke("grafana"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Revoke("grafana"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second revoke: %v", err)
	}
	// The first reader notices the change once its check interval passes.
	ts.mu.Lock()
	ts.checked = time.Time{}
	ts.mu.Unlock()
	if _, ok := ts.Verify(secret); ok {
		t.Fatal("a revoked token is still accepted")
	}

	now := time.Now()
	ts.now = func() time.Time { return now }
	short, _, err := ts.Create("ci", Operator, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ts.Verify(short); !ok {
		t.Fatal("fresh token rejected")
	}
	ts.now = func() time.Time { return now.Add(2 * time.Hour) }
	if _, ok := ts.Verify(short); ok || ts.Len() != 0 {
		t.Fatal("an expired token is accepted")
	}

	if err := os.Chmod(path, 0o644); err != nil { //nolint:gosec // the test makes the file readable by others on purpose
		t.Fatal(err)
	}
	if _, err := OpenTokens(path); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("a readable file was accepted: %v", err)
	}
}

func TestCheckSecretRejectsOddHashes(t *testing.T) {
	for _, h := range []string{"", "$argon2i$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA", "$argon2id$v=19$m=0,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=99999999,t=1,p=1$c2FsdA$aGFzaA", "$argon2id$v=19$m=8,t=1,p=1$!!$aGFzaA"} {
		if checkSecret("x", h) {
			t.Errorf("accepted %q", h)
		}
	}
}

type memSessions struct {
	mu sync.Mutex
	m  map[string]Session
}

func (s *memSessions) SaveSession(_ context.Context, x Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[x.IDHash] = x
	return nil
}

func (s *memSessions) Session(_ context.Context, h string) (Session, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, ok := s.m[h]
	return x, ok, nil
}

func (s *memSessions) TouchSession(_ context.Context, h string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	x := s.m[h]
	x.LastSeen = at
	s.m[h] = x
	return nil
}

func (s *memSessions) DeleteSession(_ context.Context, h string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, h)
	return nil
}

func (s *memSessions) DeleteSessionsBefore(_ context.Context, seen, created time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, x := range s.m {
		if x.LastSeen.Before(seen) || x.CreatedAt.Before(created) {
			delete(s.m, k)
		}
	}
	return nil
}

func TestSessions(t *testing.T) {
	ctx := context.Background()
	mem := &memSessions{m: map[string]Session{}}
	ss := NewSessions(mem)
	now := time.Now()
	ss.now = func() time.Time { return now }
	id, sess, err := ss.Create(ctx, Operator, "uid:1000")
	if err != nil {
		t.Fatal(err)
	}
	if _, stored := mem.m[id]; stored || sess.IDHash != HashID(id) || len(sess.CSRF) != 43 {
		t.Fatal("the raw identifier is stored")
	}
	got, ok, err := ss.Lookup(ctx, id)
	if err != nil || !ok || got.Role != Operator || got.Actor != "uid:1000" {
		t.Fatalf("lookup %+v %v %v", got, ok, err)
	}
	if _, ok, _ := ss.Lookup(ctx, id[:42]+"x"); ok {
		t.Fatal("a wrong identifier opened the session")
	}
	// Used every few days, a session lives a month at most.
	for d := 6; d <= 30; d += 6 {
		ss.now = func() time.Time { return now.Add(time.Duration(d) * 24 * time.Hour) }
		if _, ok, _ := ss.Lookup(ctx, id); !ok {
			t.Fatalf("session ended after %d days of regular use", d)
		}
	}
	ss.now = func() time.Time { return now.Add(31 * 24 * time.Hour) }
	if _, ok, _ := ss.Lookup(ctx, id); ok {
		t.Fatal("a session outlived its lifetime")
	}
	id2, _, _ := ss.Create(ctx, Viewer, "token:ci")
	ss.now = func() time.Time { return now.Add(31*24*time.Hour + SessionIdle + time.Minute) }
	if _, ok, _ := ss.Lookup(ctx, id2); ok {
		t.Fatal("an idle session is still valid")
	}
	id3, _, _ := ss.Create(ctx, Viewer, "token:ci")
	if err := ss.End(ctx, id3); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := ss.Lookup(ctx, id3); ok {
		t.Fatal("an ended session is still valid")
	}
}

func TestCodes(t *testing.T) {
	c := NewCodes()
	now := time.Now()
	c.now = func() time.Time { return now }
	v, err := c.Issue(Admin, "uid:1000")
	if err != nil {
		t.Fatal(err)
	}
	if role, actor, ok := c.Redeem(v); !ok || role != Admin || actor != "uid:1000" {
		t.Fatal("a fresh code was refused")
	}
	if _, _, ok := c.Redeem(v); ok {
		t.Fatal("a code worked twice")
	}
	late, _ := c.Issue(Admin, "uid:1000")
	c.now = func() time.Time { return now.Add(CodeLifetime) }
	if _, _, ok := c.Redeem(late); ok {
		t.Fatal("an expired code worked")
	}
	for range maxCodes + 10 {
		if _, err := c.Issue(Viewer, "uid:1000"); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.codes) > maxCodes {
		t.Fatalf("%d codes kept", len(c.codes))
	}
}
