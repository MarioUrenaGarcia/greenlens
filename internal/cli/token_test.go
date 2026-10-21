// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/auth"
)

func TestTokensWithoutAgent(t *testing.T) {
	cfgPath, db := offlineConfig(t)
	file := filepath.Join(filepath.Dir(db), "tokens")

	code, out, errOut := runWith(t, hooks{}, nil, "token", "list", "--config", cfgPath)
	if code != 0 || !strings.Contains(out, "There are no access tokens") {
		t.Fatalf("empty list: %d %s %s", code, out, errOut)
	}
	code, out, errOut = runWith(t, hooks{}, nil, "token", "create", "--config", cfgPath, "--name", "grafana", "--role", "viewer", "--expires", "90d", "-o", "json")
	var created api.CreatedToken
	if code != 0 || json.Unmarshal([]byte(out), &created) != nil || !strings.HasPrefix(created.Secret, auth.TokenPrefix) ||
		created.Token.ExpiresAt == nil || created.Token.ExpiresAt.Sub(created.Token.CreatedAt) != 90*24*time.Hour {
		t.Fatalf("create: %d %s %s", code, out, errOut)
	}
	data, err := os.ReadFile(file)
	if err != nil || strings.Contains(string(data), created.Secret) {
		t.Fatalf("token file: %v", err)
	}
	tokens, err := auth.OpenTokens(file)
	if err != nil {
		t.Fatal(err)
	}
	if tk, ok := tokens.Verify(created.Secret); !ok || tk.Role != auth.Viewer {
		t.Fatal("the created token does not verify")
	}
	code, out, _ = runWith(t, hooks{}, map[string]string{"GREENLENS_LANG": "es"}, "token", "create", "--config", cfgPath, "--name", "admin", "--role", "admin")
	if code != 0 || !strings.Contains(out, "no se vuelve a mostrar") || !strings.Contains(out, auth.TokenPrefix) {
		t.Fatalf("create es: %d %s", code, out)
	}
	code, out, _ = runWith(t, hooks{}, nil, "token", "list", "--config", cfgPath)
	if code != 0 || !strings.Contains(out, "grafana") || !strings.Contains(out, "admin") || strings.Contains(out, auth.TokenPrefix) {
		t.Fatalf("list: %d %s", code, out)
	}
	for _, args := range [][]string{
		{"create", "--name", "x", "--role", "root"},
		{"create", "--name", "x", "--expires", "soon"},
		{"create", "--name", "x", "--expires", "-1h"},
		{"create", "--name", "Bad Name"},
		{"create", "--name", "grafana"},
	} {
		if code, _, errOut := runWith(t, hooks{}, nil, append([]string{"token", "--config", cfgPath}, args...)...); code != 2 || !strings.Contains(errOut, "GL0005") {
			t.Errorf("%v: %d %s", args, code, errOut)
		}
	}
	if code, out, _ := runWith(t, hooks{}, nil, "token", "revoke", "--config", cfgPath, "grafana"); code != 0 || !strings.Contains(out, "revoked") {
		t.Fatalf("revoke: %d %s", code, out)
	}
	if code, _, errOut := runWith(t, hooks{}, nil, "token", "revoke", "--config", cfgPath, "grafana"); code != 1 || !strings.Contains(errOut, "GL0018") {
		t.Fatalf("second revoke: %d %s", code, errOut)
	}
}

func TestParseLifetime(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"never", 0, true}, {"0", 0, true}, {"90d", 90 * 24 * time.Hour, true}, {"2w", 14 * 24 * time.Hour, true},
		{"36h", 36 * time.Hour, true}, {"90m", 90 * time.Minute, true}, {"1h30m", 90 * time.Minute, true},
		{"0d", 0, false}, {"-1h", 0, false}, {"soon", 0, false}, {"", 0, false},
	} {
		got, err := parseLifetime(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("%q: %v %v", tt.in, got, err)
		}
	}
}
