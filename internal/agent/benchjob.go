// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/bench"
	"github.com/MarioUrenaGarcia/greenlens/internal/calibrate"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/discovery"
	"github.com/MarioUrenaGarcia/greenlens/internal/sampler"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

// States of a benchmark run by the agent.
const (
	benchIdle     = "idle"
	benchRunning  = "running"
	benchDone     = "done"
	benchFailed   = "failed"
	benchCanceled = "canceled"
)

const eventBenchmark = "benchmark"

// benchJob runs one benchmark at a time. Its meter adds up the samples the
// agent takes anyway and the energy its accountant gives the backend, so
// the benchmark does not read the sources a second time.
type benchJob struct {
	mu     sync.Mutex
	state  api.BenchmarkJob
	cancel context.CancelFunc
	done   chan struct{}

	measuring bool
	backend   string
	energy    bench.Energy
	ownTicks  uint64
	busyTicks uint64
	waiters   []chan struct{}
}

func newBenchJob() *benchJob {
	return &benchJob{state: api.BenchmarkJob{SchemaVersion: api.SchemaVersion, State: benchIdle}}
}

func (j *benchJob) status() api.BenchmarkJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state
}

// observe adds one sample to the run being measured.
func (j *benchJob) observe(s sampler.Sample, got map[string]float64, ticks map[string]uint64, busy uint64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.measuring {
		return
	}
	if j.energy.DomainsJ == nil {
		j.energy.DomainsJ = map[string]float64{}
	}
	for _, c := range s.Channels {
		j.energy.DomainsJ[string(c.Domain)] += c.EnergyJ
		if c.Domain.Level() == source.LevelComponent {
			j.energy.TotalJ += c.EnergyJ
		}
	}
	if j.backend != "" {
		j.energy.BackendJ += got[j.backend]
		j.ownTicks += ticks[j.backend]
	}
	j.busyTicks += busy
	for _, w := range j.waiters {
		close(w)
	}
	j.waiters = nil
}

func (j *benchJob) begin() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.measuring, j.energy = true, bench.Energy{}
}

// next returns a channel closed by the next sample.
func (j *benchJob) next() <-chan struct{} {
	j.mu.Lock()
	defer j.mu.Unlock()
	w := make(chan struct{})
	j.waiters = append(j.waiters, w)
	return w
}

func (j *benchJob) end() bench.Energy {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.measuring = false
	return j.energy
}

func (a *Agent) publishBenchmark() {
	if !a.stream.listening() {
		return
	}
	if data, err := json.Marshal(a.bjob.status()); err == nil {
		a.stream.publish(event{name: eventBenchmark, data: data})
	}
}

// checkBenchRequest fills the defaults of a request and rejects values a
// benchmark cannot use, with the same limits as greenlens bench.
func checkBenchRequest(req *api.BenchmarkRequest) error {
	kind := discovery.Kind(req.Kind)
	switch {
	case !slices.Contains(discovery.Kinds, kind) || kind == discovery.Process:
		return errors.New("kind must be the kind of an inference server, such as ollama or llamacpp")
	case req.Model == "" || len(req.Model) > 512:
		return errors.New("model is required")
	case req.Runs < 2 || req.Runs > 100:
		return errors.New("runs must be between 2 and 100")
	case req.MaxTokens < 1 || req.MaxTokens > 32768:
		return errors.New("max_tokens must be between 1 and 32768")
	case req.WarmupS < 0 || req.WarmupS > 600:
		return errors.New("warmup_s must be between 0 and 600")
	case req.MinRunS < 0 || req.MinRunS > 3600:
		return errors.New("min_run_s must be between 0 and 3600")
	}
	if req.Endpoint == "" {
		req.Endpoint = discovery.DefaultEndpoint(kind)
	}
	if err := config.CheckEndpoint(req.Endpoint); err != nil {
		return fmt.Errorf("endpoint %w", err)
	}
	return nil
}

// sameServer reports whether two base URLs name the same server.
func sameServer(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	if err1 != nil || err2 != nil {
		return false
	}
	ha, hb := ua.Hostname(), ub.Hostname()
	loop := func(h string) bool { return h == "localhost" || strings.HasPrefix(h, "127.") || h == "::1" }
	return ua.Port() == ub.Port() && (ha == hb || loop(ha) && loop(hb))
}

func (a *Agent) handleStartBenchmark(w http.ResponseWriter, r *http.Request) {
	var req api.BenchmarkRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "the body must be a benchmark request: "+err.Error())
		return
	}
	if err := checkBenchRequest(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if a.cjob.status().State == benchRunning {
		writeError(w, http.StatusConflict, "a calibration is running; benchmark once it ends")
		return
	}
	j := a.bjob
	j.mu.Lock()
	if j.state.State == benchRunning {
		j.mu.Unlock()
		writeError(w, http.StatusConflict, "a benchmark is already running")
		return
	}
	// The benchmark outlives the request that starts it; shutdown and
	// DELETE stop it.
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	now := time.Now().UTC()
	j.state = api.BenchmarkJob{SchemaVersion: api.SchemaVersion, State: benchRunning, Runs: req.Runs, Request: &req, StartedAt: &now}
	j.cancel, j.done = cancel, make(chan struct{})
	j.mu.Unlock()
	who := actor(r)
	go a.runBenchmark(ctx, req, who)
	a.publishBenchmark()
	writeJSON(w, http.StatusAccepted, j.status())
}

func (a *Agent) handleBenchmarkRun(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.bjob.status())
}

func (a *Agent) handleCancelBenchmark(w http.ResponseWriter, _ *http.Request) {
	j := a.bjob
	j.mu.Lock()
	running, cancel := j.state.State == benchRunning, j.cancel
	j.mu.Unlock()
	if !running {
		writeError(w, http.StatusConflict, "no benchmark is running")
		return
	}
	cancel()
	writeJSON(w, http.StatusAccepted, j.status())
}

// stopBenchmark cancels a running benchmark at shutdown and waits for it,
// so it does not write to a closed database.
func (a *Agent) stopBenchmark(ctx context.Context) {
	j := a.bjob
	j.mu.Lock()
	cancel, done := j.cancel, j.done
	j.mu.Unlock()
	if cancel == nil || done == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-ctx.Done():
		a.log.Warn("the benchmark did not stop in time")
	}
}

func (a *Agent) runBenchmark(ctx context.Context, req api.BenchmarkRequest, who string) {
	j := a.bjob
	defer func() {
		j.mu.Lock()
		j.cancel = nil
		close(j.done)
		j.mu.Unlock()
	}()
	finish := func(state string, id int64, err error) {
		j.mu.Lock()
		now := time.Now().UTC()
		j.state.State, j.state.EndedAt = state, &now
		if id > 0 {
			j.state.BenchmarkID = id
		}
		if err != nil {
			j.state.Error = err.Error()
		}
		j.measuring = false
		j.mu.Unlock()
		a.publishBenchmark()
	}

	a.mu.RLock()
	calibrated := a.calib != nil
	var idleW float64
	if a.calib != nil {
		b := a.calib.Baseline()
		idleW = b.CPUW + b.DRAMW
		for _, g := range b.GPUW {
			idleW += g
		}
	}
	interval := a.cfg.Agent.SampleInterval.D()
	var channels []string
	for _, c := range a.sel.Channels {
		channels = append(channels, c.ID)
	}
	a.mu.RUnlock()
	a.bmu.Lock()
	backend := ""
	for _, b := range a.bs {
		if b.Endpoint != "" && sameServer(b.Endpoint, req.Endpoint) {
			backend = b.Name
		}
	}
	a.bmu.Unlock()
	basis := bench.BasisTotal
	switch {
	case backend != "":
		basis = bench.BasisBackend
	case calibrated:
		basis = bench.BasisDynamic
	}
	j.mu.Lock()
	j.backend, j.ownTicks, j.busyTicks = backend, 0, 0
	j.mu.Unlock()

	bctx := bench.Snapshot(a.hostFS)
	bctx.Channels = channels
	throttle0, hasThrottle := bench.Throttles(a.hostFS)
	meter := func(fn func() error) (bench.Energy, time.Duration, error) {
		j.begin()
		start := time.Now()
		err := fn()
		d := time.Since(start)
		// The sample that covers the end of the run arrives within an
		// interval.
		select {
		case <-j.next():
		case <-time.After(2 * interval):
		case <-ctx.Done():
		}
		e := j.end()
		e.IdleJ = idleW * d.Seconds()
		return e, d, err
	}
	// Seed and temperature stay those of greenlens bench, so results of the
	// agent and of the command line compare.
	params := bench.DefaultParams()
	params.MaxTokens, params.Runs, params.WarmupS, params.MinRunS = req.MaxTokens, req.Runs, req.WarmupS, req.MinRunS
	target := bench.Target{Kind: discovery.Kind(req.Kind), Endpoint: req.Endpoint, Model: req.Model}
	client := &http.Client{Timeout: 10 * time.Minute}
	res, err := bench.Bench(ctx, client, target, params, bench.DefaultPrompts(), meter, basis, calibrated, func(run int) {
		j.mu.Lock()
		j.state.Run = run
		j.mu.Unlock()
		a.publishBenchmark()
	})
	if errors.Is(err, context.Canceled) {
		finish(benchCanceled, 0, nil)
		return
	}
	if err != nil {
		a.log.Warn("benchmark failed", "model", req.Model, "error", err)
		finish(benchFailed, 0, err)
		return
	}
	bctx.EndTempC = calibrate.Temperature(a.hostFS)
	if throttle1, ok := bench.Throttles(a.hostFS); ok && hasThrottle {
		t := throttle1 > throttle0
		bctx.Throttled = &t
	}
	j.mu.Lock()
	if backend != "" && j.busyTicks > 0 {
		bctx.OtherCPUShare = max(0, 1-float64(j.ownTicks)/float64(j.busyTicks))
	}
	j.mu.Unlock()
	res.Context = bctx
	res.Causes = bench.Diagnose(res)
	id, err := a.st.SaveBenchmark(ctx, res)
	if err != nil {
		a.log.Error("saving the benchmark failed", "error", err)
		finish(benchFailed, 0, errors.New("saving the benchmark failed"))
		return
	}
	if err := a.st.Audit(ctx, who, "benchmark", fmt.Sprintf("%s %s, %d runs", req.Kind, req.Model, len(res.Runs))); err != nil {
		a.log.Warn("audit log write failed", "error", err)
	}
	finish(benchDone, id, nil)
}
