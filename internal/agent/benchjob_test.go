// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/bench"
)

// benchServer answers like Ollama to any prompt, slowly enough for the
// agent to take samples during each run.
func benchServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/generate", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		time.Sleep(15 * time.Millisecond)
		_, _ = io.WriteString(w, `{"model":"qwen2.5:0.5b","response":"ok","done":true,"prompt_eval_count":12,"eval_count":8}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func waitBenchmark(t *testing.T, c call) api.BenchmarkJob {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, body := c.do("GET", api.PathBenchRun, nil)
		var run api.BenchmarkJob
		if resp.code != 200 || json.Unmarshal(body, &run) != nil {
			t.Fatalf("run: %d %s", resp.code, body)
		}
		if run.State != "running" {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("benchmark still running: %+v", run)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestBenchmarkInTheAgent(t *testing.T) {
	r := startAgent(t, "")
	waitSamples(t, r, 2)
	sock := call{t: t, c: socketClient(r.socket), base: "http://agent"}
	up := benchServer(t)
	resp, body := sock.do("POST", api.PathBenchmarks, api.BenchmarkRequest{Kind: "ollama", Endpoint: up.URL, Model: "qwen2.5:0.5b", Runs: 3, MaxTokens: 16})
	if resp.code != http.StatusAccepted {
		t.Fatalf("start: %d %s", resp.code, body)
	}
	run := waitBenchmark(t, sock)
	if run.State != "done" || run.Run != 3 || run.EndedAt == nil {
		t.Fatalf("run %+v", run)
	}
	b, err := r.client.Benchmark(t.Context(), run.BenchmarkID)
	if err != nil {
		t.Fatal(err)
	}
	// No backend of the agent serves the fake endpoint, so the energy of
	// the node is the basis.
	if len(b.Runs) != 3 || b.GeneratedTokens <= 0 || b.Basis == bench.BasisBackend || b.JPerToken.Mean <= 0 || len(b.Context.Channels) == 0 {
		t.Fatalf("benchmark %+v", b)
	}
	for _, x := range b.Runs {
		if x.EnergyJ <= 0 || x.DomainsJ["package"] <= 0 {
			t.Fatalf("run without energy: %+v", x)
		}
	}

	// A canceled benchmark stops and stores nothing.
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer slow.Close()
	if resp, body := sock.do("POST", api.PathBenchmarks, api.BenchmarkRequest{Kind: "ollama", Endpoint: slow.URL, Model: "m", Runs: 2, MaxTokens: 4}); resp.code != http.StatusAccepted {
		t.Fatalf("start: %d %s", resp.code, body)
	}
	if resp, _ := sock.do("DELETE", api.PathBenchRun, nil); resp.code != http.StatusAccepted {
		t.Fatalf("cancel: %d", resp.code)
	}
	if run := waitBenchmark(t, sock); run.State != "canceled" || run.BenchmarkID != 0 {
		t.Fatalf("canceled run %+v", run)
	}
}

func TestSameServer(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want bool
	}{
		{"http://127.0.0.1:11434", "http://localhost:11434", true},
		{"http://127.0.0.1:11434/", "http://127.0.0.1:11434", true},
		{"http://127.0.0.1:11434", "http://127.0.0.1:8080", false},
		{"http://192.0.2.1:11434", "http://127.0.0.1:11434", false},
	} {
		if got := sameServer(tt.a, tt.b); got != tt.want {
			t.Errorf("%s %s: %v", tt.a, tt.b, got)
		}
	}
}
