// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/client"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

// liveFS is a host whose files change while the agent reads them.
type liveFS struct {
	mu    sync.Mutex
	files fstest.MapFS
}

func (l *liveFS) Open(name string) (fs.File, error) {
	l.mu.Lock()
	snap := maps.Clone(l.files)
	l.mu.Unlock()
	return snap.Open(name)
}

func (l *liveFS) set(name, data string) {
	l.mu.Lock()
	l.files[name] = &fstest.MapFile{Data: []byte(data)}
	l.mu.Unlock()
}

// llamaServer answers as llama-server with --metrics: every scrape finds
// 40 more prompt and 20 more predicted tokens.
func llamaServer(t *testing.T) (*httptest.Server, int) {
	t.Helper()
	var scrapes atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		n := scrapes.Add(1)
		_, _ = fmt.Fprintf(w, "# TYPE llamacpp:prompt_tokens_total counter\nllamacpp:prompt_tokens_total %d\n", 40*n)
		_, _ = fmt.Fprintf(w, "# TYPE llamacpp:tokens_predicted_total counter\nllamacpp:tokens_predicted_total %d\n", 20*n)
		_, _ = fmt.Fprint(w, "# TYPE llamacpp:requests_processing gauge\nllamacpp:requests_processing 1\n")
	})
	mux.HandleFunc("GET /props", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"model_path":"/models/qwen2.5-0.5b-instruct-q4_k_m.gguf"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	_, p, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		t.Fatal(err)
	}
	return srv, port
}

func procStat(pid, ppid int, utime uint64) string {
	return fmt.Sprintf("%d (llama-server) S %d 0 0 0 -1 0 0 0 0 0 %d 0 0 0 20 0 1 0 500 1000 100\n", pid, ppid, utime)
}

func TestBackendEnergyAndTokens(t *testing.T) {
	_, port := llamaServer(t)
	host := &liveFS{files: fstest.MapFS{
		"proc/stat":                     {Data: []byte("cpu 0 0 0 1000 0 0 0 0 0 0\n")},
		"proc/4242/stat":                {Data: []byte(procStat(4242, 1, 0))},
		"proc/4242/cmdline":             {Data: []byte("/opt/llama.cpp/bin/llama-server\x00-m\x00q.gguf\x00--port\x00" + strconv.Itoa(port) + "\x00--metrics\x00")},
		"proc/net/tcp":                  {Data: []byte(fmt.Sprintf("  sl  local_address rem_address   st\n   0: 0100007F:%04X 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1 1\n", port))},
		"sys/devices/system/cpu/online": {Data: []byte("0\n")},
	}}
	// The system is busy 10 ticks every 10 ms and llama-server half of it.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		var busy, own uint64
		for {
			select {
			case <-stop:
				return
			case <-time.After(10 * time.Millisecond):
			}
			busy, own = busy+10, own+5
			host.set("proc/stat", fmt.Sprintf("cpu %d 0 0 1000 0 0 0 0 0 0\n", busy))
			host.set("proc/4242/stat", procStat(4242, 1, own))
		}
	})
	t.Cleanup(func() { close(stop); wg.Wait() })

	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Agent.SampleInterval = config.Duration(50 * time.Millisecond)
	cfg.Agent.DataDir = filepath.Join(dir, "data")
	cfg.Discovery.Interval = config.Duration(time.Second)
	sockDir, err := os.MkdirTemp("", "gl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	cfg.Agent.Socket = filepath.Join(sockDir, "a.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		done <- Run(ctx, Options{
			Config: cfg, Getenv: func(string) string { return "" },
			Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
			Sources: []source.Source{&fake{}}, HostFS: host,
			NoSandbox: true, Ready: ready, UsageInterval: 300 * time.Millisecond, RegressionEvery: 200 * time.Millisecond,
		})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("agent returned %v", err)
		}
	})
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("agent exited: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("agent did not start")
	}
	c := client.New(cfg.Agent.Socket)
	name := "llamacpp-" + strconv.Itoa(port)
	var b api.Backend
	deadline := time.Now().Add(15 * time.Second)
	for {
		bs, err := c.Backends(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range bs.Backends {
			if x.Name == name {
				b = x
			}
		}
		if len(b.Models) > 0 && b.Models[0].GeneratedTokens > 0 && b.EnergyJ > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("backend never measured: %+v", bs)
		}
		time.Sleep(100 * time.Millisecond)
	}
	m := b.Models[0]
	if b.Kind != "llamacpp" || b.Processes != 1 || !b.TokenCounters || b.Problem != nil || m.Name != "qwen2.5-0.5b-instruct-q4_k_m.gguf" {
		t.Fatalf("backend %+v", b)
	}
	// The fake package draws 200 W and the backend uses half of the busy
	// time, so it gets about 100 W.
	if b.CPUJ <= 0 || m.JoulesPerToken <= 0 || m.Method != "attributed_cpu_share" {
		t.Fatalf("energy %+v, model %+v", b, m)
	}
	st, err := c.Status(context.Background())
	if err != nil || len(st.Backends) != 1 {
		t.Fatalf("status backends %+v, %v", st.Backends, err)
	}
	now := time.Now().UTC()
	u, err := c.BackendUsage(context.Background(), now.Add(-time.Minute), now.Add(time.Minute), store.ByModel)
	if err != nil || len(u.Rows) != 1 || u.Rows[0].Model != m.Name || u.Rows[0].GeneratedTokens <= 0 || u.Rows[0].EnergyJ <= 0 {
		t.Fatalf("usage %+v, %v", u, err)
	}
}
