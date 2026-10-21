// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
)

const testLink = "http://127.0.0.1:10060/login?code=abc"

// fakeAgent answers the health check and the sign-in codes on a socket, with
// the HTTP API on or off. It returns the path of a configuration that
// points at it and a function that lists the roles asked for.
func fakeAgent(t *testing.T, httpOn bool) (string, func() []string) {
	t.Helper()
	sockDir, err := os.MkdirTemp("", "gl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "a.sock")
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var roles []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+api.PathHealth, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{}")) })
	mux.HandleFunc("POST "+api.PathSessionCodes, func(w http.ResponseWriter, r *http.Request) {
		var req api.SessionCodeRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		roles = append(roles, req.Role)
		mu.Unlock()
		if !httpOn {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"the HTTP API is disabled; set server.enabled"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(api.SessionCode{SchemaVersion: api.SchemaVersion, URL: testLink, ExpiresAt: time.Now().Add(2 * time.Minute)})
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	cfgPath := filepath.Join(t.TempDir(), "greenlens.yaml")
	if err := os.WriteFile(cfgPath, []byte("agent:\n  socket: "+sock+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(roles)
	}
}

func TestGUIOpensOrPrintsTheLink(t *testing.T) {
	cfgPath, roles := fakeAgent(t, true)
	var opened []string
	h := hooks{open: func(link string) error { opened = append(opened, link); return nil }}

	desktop := map[string]string{"WAYLAND_DISPLAY": "wayland-0"}
	code, out, errOut := runWith(t, h, desktop, "gui", "--config", cfgPath)
	if code != 0 || len(opened) != 1 || opened[0] != testLink || !strings.Contains(out, "Opening GreenLens") || !strings.Contains(out, testLink) {
		t.Fatalf("open: %d %v %s %s", code, opened, out, errOut)
	}
	if roles()[0] != "admin" {
		t.Fatalf("default role %v", roles())
	}

	code, out, _ = runWith(t, h, desktop, "gui", "--config", cfgPath, "--print", "--role", "viewer")
	if code != 0 || out != testLink+"\n" || len(opened) != 1 || roles()[1] != "viewer" {
		t.Fatalf("print: %d %q %v", code, out, roles())
	}

	// Over SSH the browser would open on the server: the link comes with
	// the port forward, even with X forwarding.
	ssh := map[string]string{"DISPLAY": "localhost:10.0", "SSH_CONNECTION": "192.0.2.10 50000 192.0.2.20 22"}
	code, out, _ = runWith(t, h, ssh, "gui", "--config", cfgPath)
	if code != 0 || len(opened) != 1 || !strings.Contains(out, "ssh -N -L 10060:127.0.0.1:10060 <server>") || !strings.Contains(out, testLink) {
		t.Fatalf("ssh: %d %s", code, out)
	}
	code, out, _ = runWith(t, h, map[string]string{"SSH_TTY": "/dev/pts/1", "GREENLENS_LANG": "es"}, "gui", "--config", cfgPath)
	if code != 0 || !strings.Contains(out, "<servidor>") || !strings.Contains(out, "funciona una vez") {
		t.Fatalf("ssh es: %d %s", code, out)
	}

	// No graphical session, or no program for links: the link is printed.
	code, out, _ = runWith(t, h, nil, "gui", "--config", cfgPath)
	if code != 0 || len(opened) != 1 || strings.Contains(out, "ssh -N") || !strings.Contains(out, "Visit this link") {
		t.Fatalf("headless: %d %s", code, out)
	}
	failing := hooks{open: func(string) error { return errors.New("no handler") }}
	code, out, _ = runWith(t, failing, desktop, "gui", "--config", cfgPath)
	if code != 0 || !strings.Contains(out, "Visit this link") || !strings.Contains(out, testLink) {
		t.Fatalf("open failed: %d %s", code, out)
	}

	code, out, _ = runWith(t, h, nil, "gui", "--config", cfgPath, "-o", "json")
	var doc api.SessionCode
	if code != 0 || json.Unmarshal([]byte(out), &doc) != nil || doc.URL != testLink {
		t.Fatalf("json: %d %s", code, out)
	}
	if code, _, errOut := runWith(t, h, nil, "gui", "--config", cfgPath, "--role", "root"); code != 2 || !strings.Contains(errOut, "GL0005") {
		t.Fatalf("bad role: %d %s", code, errOut)
	}
}

func TestGUIReportsAnAgentWithoutHTTP(t *testing.T) {
	cfgPath, _ := fakeAgent(t, false)
	var notes []string
	h := hooks{
		open:   func(string) error { t.Error("opened a browser"); return nil },
		notify: func(title, body string) error { notes = append(notes, title+": "+body); return nil },
	}
	code, _, errOut := runWith(t, h, nil, "gui", "--config", cfgPath)
	if code != 6 || !strings.Contains(errOut, "GL4015") || !strings.Contains(errOut, "server.enabled") || len(notes) != 0 {
		t.Fatalf("http off: %d %s %v", code, errOut, notes)
	}
	// From the desktop launcher the error also becomes a notification.
	code, _, _ = runWith(t, h, map[string]string{"DISPLAY": ":0"}, "gui", "--config", cfgPath)
	if code != 6 || len(notes) != 1 || !strings.Contains(notes[0], "graphical interface is off") {
		t.Fatalf("notification: %d %v", code, notes)
	}
	missing := filepath.Join(t.TempDir(), "greenlens.yaml")
	if err := os.WriteFile(missing, []byte("agent:\n  socket: "+filepath.Join(t.TempDir(), "none.sock")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runWith(t, h, nil, "gui", "--config", missing); code != 1 || !strings.Contains(errOut, "GL4001") {
		t.Fatalf("no agent: %d %s", code, errOut)
	}
}
