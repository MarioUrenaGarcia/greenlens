// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/MarioUrenaGarcia/greenlens/internal/client"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/sourcetest"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

// Text that must never leave the proxy: the prompt, the generated text
// and the key of the consumer.
const (
	promptSentinel   = "PROMPT-SENTINEL-5e1f"
	responseSentinel = "RESPONSE-SENTINEL-77aa"
	headerSentinel   = "HEADER-SENTINEL-c0de"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func fakeOllama(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"version":"0.40.6"}`)
	})
	mux.HandleFunc("GET /api/ps", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"models":[{"name":"qwen2.5:0.5b"}]}`)
	})
	mux.HandleFunc("POST /api/generate", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(promptSentinel)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		for range 5 {
			_, _ = fmt.Fprintf(w, `{"model":"qwen2.5:0.5b","response":"%s","done":false}`+"\n", responseSentinel)
			w.(http.Flusher).Flush()
			time.Sleep(20 * time.Millisecond)
		}
		_, _ = io.WriteString(w, `{"model":"qwen2.5:0.5b","response":"","done":true,"prompt_eval_count":31,"eval_count":5}`+"\n")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func freePort(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestProxyKeepsNoContent(t *testing.T) {
	up := fakeOllama(t)
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Agent.SampleInterval = config.Duration(50 * time.Millisecond)
	cfg.Agent.DataDir = filepath.Join(dir, "data")
	sockDir, err := os.MkdirTemp("", "gl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	cfg.Agent.Socket = filepath.Join(sockDir, "a.sock")
	cfg.Backends = []config.Backend{{Name: "ollama-test", Kind: "ollama", Endpoint: up.URL}}
	cfg.Proxy = config.Proxy{Enabled: true, Listen: freePort(t), Upstream: up.URL, PerRequestEnergyHeader: true, ConsumerHeader: "Authorization"}

	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		done <- Run(ctx, Options{
			Config: cfg, Getenv: func(string) string { return "" },
			Logger:  slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
			Sources: []source.Source{&fake{}}, HostFS: sourcetest.Host(t, "amd-zen4-desktop"),
			NoSandbox: true, Ready: ready, UsageInterval: 200 * time.Millisecond,
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("agent exited: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("agent did not start")
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		if err := <-done; err != nil {
			t.Errorf("agent returned %v", err)
		}
	}
	t.Cleanup(stop)

	for range 3 {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://"+cfg.Proxy.Listen+"/api/generate", strings.NewReader(`{"model":"qwen2.5:0.5b","prompt":"`+promptSentinel+`"}`))
		req.Header.Set("Authorization", "Bearer "+headerSentinel)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if !bytes.Contains(body, []byte(responseSentinel)) || resp.Trailer.Get("X-GreenLens-Energy-Joules") == "" {
			t.Fatalf("response %q, trailer %v", body, resp.Trailer)
		}
	}

	c := client.New(cfg.Agent.Socket)
	deadline := time.Now().Add(10 * time.Second)
	for {
		now := time.Now().UTC()
		u, err := c.BackendUsage(context.Background(), now.Add(-time.Minute), now.Add(time.Minute), store.ByConsumer)
		if err != nil {
			t.Fatal(err)
		}
		if len(u.Rows) == 1 && u.Rows[0].GeneratedTokens == 15 && u.Rows[0].PromptTokens == 93 {
			if !strings.HasPrefix(u.Rows[0].Consumer, "consumer-") || u.Rows[0].Backend != "ollama-test" {
				t.Fatalf("row %+v", u.Rows[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tokens of the proxy never stored: %+v", u.Rows)
		}
		time.Sleep(100 * time.Millisecond)
	}
	st, err := c.Status(context.Background())
	if err != nil || st.Proxy == nil || st.Proxy.Requests != 3 || st.Proxy.Backend != "ollama-test" {
		t.Fatalf("proxy status %+v %v", st.Proxy, err)
	}
	stop()

	// The key is stored only as an HMAC.
	db, err := sql.Open("sqlite", filepath.Join(cfg.Agent.DataDir, "greenlens.db"))
	if err != nil {
		t.Fatal(err)
	}
	var alias, h string
	if err := db.QueryRowContext(context.Background(), `SELECT alias, key_hmac FROM consumers`).Scan(&alias, &h); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(h) || alias != "consumer-"+h[:12] {
		t.Fatalf("consumer %q %q", alias, h)
	}

	// Nothing the client sent or received is in the logs or on disk.
	check := func(where string, data []byte) {
		for _, s := range []string{promptSentinel, responseSentinel, headerSentinel} {
			if bytes.Contains(data, []byte(s)) {
				t.Errorf("%s contains %s", where, s)
			}
		}
	}
	check("the logs", []byte(logs.String()))
	err = filepath.WalkDir(cfg.Agent.DataDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p) //nolint:gosec // files the test created
		if err != nil {
			return err
		}
		check(p, data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
