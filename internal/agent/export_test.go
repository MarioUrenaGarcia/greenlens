// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/exporter/otlp"
)

// collector records the OTLP requests it receives.
type collector struct {
	mu     sync.Mutex
	bodies [][]byte
	auth   string
	path   string
	status int
}

func (c *collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bodies, c.auth, c.path = append(c.bodies, body), r.Header.Get("Authorization"), r.URL.Path
	if c.status != 0 {
		w.WriteHeader(c.status)
	}
}

func (c *collector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.bodies)
}

func waitExports(t *testing.T, c *collector, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.count() < n {
		if time.Now().After(deadline) {
			t.Fatalf("%d exports after 5 seconds, want %d", c.count(), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestExportsMetricsWithOTLP(t *testing.T) {
	col := &collector{}
	srv := httptest.NewServer(col)
	defer srv.Close()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("collector-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := startAgentWith(t, "", func(c *config.Config) {
		c.Export.OTLP = config.OTLP{Enabled: true, Endpoint: srv.URL, Protocol: otlp.ProtocolHTTP,
			Interval: config.Duration(50 * time.Millisecond), TokenFile: tokenFile}
	})
	waitSamples(t, r, 2)
	waitExports(t, col, 2)
	col.mu.Lock()
	body, auth, path := col.bodies[len(col.bodies)-1], col.auth, col.path
	col.mu.Unlock()
	if path != "/v1/metrics" || auth != "Bearer collector-token" {
		t.Fatalf("%s with %q", path, auth)
	}
	for _, want := range []string{"service.name", "greenlens_node_energy_joules_total", "greenlens_build_info", "fake"} {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("the request lacks %q", want)
		}
	}
	// The export counts itself among the collectors, as /metrics shows.
	text := scrape(t, r)
	if !bytes.Contains([]byte(text), []byte(`greenlens_collector_errors_total{collector="otlp"} 0`)) {
		t.Errorf("the export is not among the collectors:\n%s", text)
	}
	// Stopping sends the final values.
	before := col.count()
	r.stop(t)
	if col.count() <= before {
		t.Errorf("no export while stopping: %d before, %d after", before, col.count())
	}
}

func TestExportFailuresAreCounted(t *testing.T) {
	col := &collector{status: http.StatusServiceUnavailable}
	srv := httptest.NewServer(col)
	defer srv.Close()
	r := startAgentWith(t, "", func(c *config.Config) {
		c.Export.OTLP = config.OTLP{Enabled: true, Endpoint: srv.URL, Protocol: otlp.ProtocolHTTP, Interval: config.Duration(50 * time.Millisecond)}
	})
	waitExports(t, col, 2)
	deadline := time.Now().Add(5 * time.Second)
	for {
		text := scrape(t, r)
		if bytes.Contains([]byte(text), []byte(`greenlens_collector_errors_total{collector="otlp"} 0`)) || !bytes.Contains([]byte(text), []byte(`collector="otlp"`)) {
			if time.Now().After(deadline) {
				t.Fatalf("failed exports are not counted:\n%s", text)
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		break
	}
}

func TestExportIsOffByDefault(t *testing.T) {
	r := startAgent(t, "")
	waitSamples(t, r, 2)
	text := scrape(t, r)
	if bytes.Contains([]byte(text), []byte(`collector="otlp"`)) {
		t.Fatal("the export ran without being turned on")
	}
}

// scrape returns the text of /metrics.
func scrape(t *testing.T, r *running) string {
	t.Helper()
	_, body := call{t: t, c: socketClient(r.socket), base: "http://agent"}.do("GET", api.PathMetrics, nil)
	return string(body)
}
