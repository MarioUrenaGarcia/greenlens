// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"context"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/calibrate"
	"github.com/MarioUrenaGarcia/greenlens/internal/client"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/sourcetest"
)

// fake reports a counter that grows by 10 J per read, and a 3 W platform.
type fake struct {
	mu sync.Mutex
	j  float64
}

func (f *fake) ID() string    { return "fake" }
func (f *fake) Priority() int { return 1 }
func (f *fake) Close() error  { return nil }
func (f *fake) Probe(context.Context) (source.ProbeResult, error) {
	return source.ProbeResult{Source: "fake", Channels: []source.Channel{
		{ID: "fake:package-0", Source: "fake", Domain: source.DomainPackage, Instance: "0", Quality: source.QualityMeasured, Method: source.MethodMeasured, Kind: source.KindCounter},
		{ID: "fake:platform", Source: "fake", Domain: source.DomainPlatform, Quality: source.QualityPlatform, Method: source.MethodIntegrated, Kind: source.KindPower},
	}}, nil
}
func (f *fake) Read(context.Context) ([]source.Reading, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.j += 10
	return []source.Reading{{Channel: "fake:package-0", EnergyJ: f.j}, {Channel: "fake:platform", PowerW: 3}}, nil
}

type running struct {
	cfg    config.Config
	cancel context.CancelFunc
	done   chan error
	reload chan struct{}
	socket string
	client *client.Client
}

func startAgent(t *testing.T, cfgPath string) *running {
	t.Helper()
	return startAgentWith(t, cfgPath, nil)
}

// startAgentWith starts an agent whose HTTP API is off unless change turns
// it on, so tests never compete for its default port.
func startAgentWith(t *testing.T, cfgPath string, change func(*config.Config)) *running {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Agent.SampleInterval = config.Duration(50 * time.Millisecond)
	cfg.Agent.DataDir = filepath.Join(dir, "data")
	// Unix socket paths are limited to about 100 bytes, shorter than many
	// temporary directories.
	sockDir, err := os.MkdirTemp("", "gl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	cfg.Agent.Socket = filepath.Join(sockDir, "a.sock")
	cfg.Server.Enabled = false
	if change != nil {
		change(&cfg)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{cfg: cfg, cancel: cancel, done: make(chan error, 1), reload: make(chan struct{}), socket: cfg.Agent.Socket}
	ready := make(chan struct{})
	go func() {
		r.done <- Run(ctx, Options{
			Config: cfg, ConfigPath: cfgPath, Getenv: func(string) string { return "" },
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Version: "test",
			Sources: []source.Source{&fake{}}, HostFS: sourcetest.Host(t, "amd-zen4-desktop"),
			NoSandbox: true, Ready: ready, Reload: r.reload, AlertInterval: 100 * time.Millisecond,
		})
	}()
	select {
	case <-ready:
	case err := <-r.done:
		t.Fatalf("agent exited: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("agent did not start")
	}
	r.client = client.New(cfg.Agent.Socket)
	t.Cleanup(func() { r.stop(t) })
	return r
}

func (r *running) stop(t *testing.T) {
	if r.cancel == nil {
		return
	}
	start := time.Now()
	r.cancel()
	r.cancel = nil
	select {
	case err := <-r.done:
		if err != nil {
			t.Errorf("agent returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not stop within 5 seconds")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("shutdown took %s", d)
	}
}

func TestAgentEndToEnd(t *testing.T) {
	r := startAgent(t, "")
	ctx := context.Background()
	st := waitSamples(t, r, 4)
	if st.SchemaVersion != 1 || st.Version != "test" || st.Estimated {
		t.Fatalf("status %+v", st)
	}
	var pkg, plat bool
	for _, d := range st.Domains {
		switch d.Domain {
		case source.DomainPackage:
			pkg = d.PowerW > 0 && d.EnergyJ > 0 && d.Level == "component"
		case source.DomainPlatform:
			plat = d.Level == "system" && d.Quality == source.QualityPlatform
		default:
			t.Errorf("unexpected domain %s", d.Domain)
		}
	}
	if !pkg || !plat {
		t.Fatalf("domains %+v", st.Domains)
	}
	if st.Self.RSSBytes == 0 {
		t.Error("self RSS not reported")
	}
	live, err := r.client.Live(ctx)
	if err != nil || len(live.Points) < 4 || math.Abs(live.Points[0].PowerW[source.DomainPlatform]-3) > 1e-9 {
		t.Fatalf("live %+v %v", live, err)
	}

	cal := calibrate.Result{StartedAt: time.Now(), Duration: time.Minute, Samples: 60,
		Domains: map[source.Domain]calibrate.Stats{source.DomainPackage: {P10: 1, P50: 2, P90: 3}}}
	if err := r.client.SaveCalibration(ctx, cal); err != nil {
		t.Fatal(err)
	}
	st, err = r.client.Status(ctx)
	if err != nil || st.Calibration == nil || st.Calibration.Baseline.CPUW != 2 {
		t.Fatalf("calibration not applied: %+v %v", st.Calibration, err)
	}
	// Seconds of samples are no estimate of idle power to compare.
	if st.Calibration.Drift != 0 || st.Calibration.IdleNowW != 0 {
		t.Errorf("drift reported after %s: %+v", time.Since(st.StartedAt), st.Calibration)
	}
	bad := cal
	bad.Samples = 0
	if err := r.client.SaveCalibration(ctx, bad); help.As(err).Code != help.GL4006 {
		t.Errorf("invalid calibration accepted: %v", err)
	}

	u, err := r.client.Usage(ctx, time.Now().Add(-time.Hour), time.Now().Add(time.Minute), 0)
	if err != nil || u.Resolution != "raw" || len(u.Rows) != 2 {
		t.Fatalf("usage %+v %v", u, err)
	}
	if _, err := r.client.Usage(ctx, time.Now(), time.Now().Add(-time.Hour), 0); help.As(err).Code != help.GL4006 {
		t.Errorf("reversed range accepted: %v", err)
	}
}

func waitSamples(t *testing.T, r *running, n uint64) (st api.Status) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s, err := r.client.Status(context.Background())
		if err == nil && s.Samples >= n {
			return s
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("fewer than %d samples after 10 s", n)
	return st
}

func TestSecondAgentIsRefused(t *testing.T) {
	r := startAgent(t, "")
	cfg := r.cfg
	cfg.Agent.DataDir = filepath.Join(t.TempDir(), "other")
	err := Run(context.Background(), Options{
		Config: cfg, Getenv: func(string) string { return "" }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Sources: []source.Source{&fake{}}, HostFS: sourcetest.Host(t, "amd-zen4-desktop"), NoSandbox: true,
	})
	if help.As(err).Code != help.GL4005 {
		t.Fatalf("err %v, want GL4005", err)
	}
	r.stop(t)
	if _, err := os.Stat(r.socket); !os.IsNotExist(err) {
		t.Errorf("socket left behind: %v", err)
	}
}

func TestReloadChangesSelection(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "greenlens.yaml")
	write := func(s string) {
		if err := os.WriteFile(cfgPath, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := startAgent(t, cfgPath)
	base := "agent:\n  sample_interval: 50ms\n  data_dir: " + r.cfg.Agent.DataDir + "\n  socket: " + r.socket + "\n"
	write(base + "sources:\n  disable: [estimate]\n")
	r.reload <- struct{}{}
	// An invalid file is rejected and the agent keeps running.
	write(base + "sources:\n  disable: [nonsense]\n")
	r.reload <- struct{}{}
	st := waitSamples(t, r, 2)
	if len(st.Channels) == 0 {
		t.Fatal("no channels after reload")
	}
	for _, c := range st.Channels {
		if strings.HasPrefix(c.ID, "fake:") && !c.Selected {
			t.Errorf("%s deselected by a reload that did not touch it", c.ID)
		}
	}
}
