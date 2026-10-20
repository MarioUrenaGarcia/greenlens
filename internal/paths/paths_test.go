// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package paths

import (
	"errors"
	"slices"
	"testing"

	"github.com/MarioUrenaGarcia/greenlens/internal/config"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDataDir(t *testing.T) {
	cfg := config.Defaults()
	for want, e := range map[string]map[string]string{
		"/var/lib/greenlens":             {"STATE_DIRECTORY": "/var/lib/greenlens:/var/lib/other", "HOME": "/home/u"},
		"/x/greenlens":                   {"XDG_DATA_HOME": "/x", "HOME": "/home/u"},
		"/home/u/.local/share/greenlens": {"HOME": "/home/u"},
	} {
		if got, err := DataDir(cfg, env(e)); err != nil || got != want {
			t.Errorf("DataDir(%v) = %q, %v; want %q", e, got, err, want)
		}
	}
	cfg.Agent.DataDir = "/srv/gl"
	if got, _ := Database(cfg, env(nil)); got != "/srv/gl/greenlens.db" {
		t.Errorf("configured: %q", got)
	}
	if _, err := DataDir(config.Defaults(), env(nil)); err == nil {
		t.Error("no home accepted")
	}
}

func TestSockets(t *testing.T) {
	cfg := config.Defaults()
	if got, _ := AgentSocket(cfg, env(map[string]string{"RUNTIME_DIRECTORY": "/run/greenlens"})); got != SystemSocket {
		t.Errorf("system socket %q", got)
	}
	if got, _ := AgentSocket(cfg, env(map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"})); got != "/run/user/1000/greenlens/greenlens.sock" {
		t.Errorf("user socket %q", got)
	}
	if _, err := AgentSocket(cfg, env(nil)); !errors.Is(err, ErrNoRuntimeDir) {
		t.Errorf("err %v", err)
	}
	if got := ClientSockets(cfg, env(map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"})); !slices.Equal(got, []string{"/run/user/1000/greenlens/greenlens.sock", SystemSocket}) {
		t.Errorf("client sockets %v", got)
	}
	cfg.Agent.Socket = "/tmp/g.sock"
	if got := ClientSockets(cfg, env(nil)); !slices.Equal(got, []string{"/tmp/g.sock"}) {
		t.Errorf("configured client socket %v", got)
	}
}
