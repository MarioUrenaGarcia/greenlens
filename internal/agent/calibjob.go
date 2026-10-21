// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/calibrate"
	"github.com/MarioUrenaGarcia/greenlens/internal/sampler"
)

// Limits of a calibration run by the agent.
const (
	maxCalibration     = 10 * time.Minute
	defaultCalibration = calibrate.DefaultDuration
	eventCalibration   = "calibration"
)

// minCalibration is the shortest calibration; tests shorten it.
var minCalibration = 10 * time.Second

// calJob measures idle power from the samples the agent takes anyway, as
// greenlens calibrate does with its own.
type calJob struct {
	mu        sync.Mutex
	state     api.CalibrationJob
	collector *calibrate.Collector
	cancel    context.CancelFunc
	done      chan struct{}
}

func newCalJob() *calJob {
	return &calJob{state: api.CalibrationJob{SchemaVersion: api.SchemaVersion, State: benchIdle}}
}

func (j *calJob) status() api.CalibrationJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state
}

func (j *calJob) observe(s sampler.Sample) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.collector != nil {
		j.collector.Add(s)
	}
}

func (a *Agent) publishCalibration() {
	if !a.stream.listening() {
		return
	}
	if data, err := json.Marshal(a.cjob.status()); err == nil {
		a.stream.publish(event{name: eventCalibration, data: data})
	}
}

func (a *Agent) handleStartCalibration(w http.ResponseWriter, r *http.Request) {
	var req api.CalibrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "the body must be JSON with duration_s")
		return
	}
	d := defaultCalibration
	if req.DurationS != 0 {
		d = time.Duration(req.DurationS * float64(time.Second))
	}
	if d < minCalibration || d > maxCalibration {
		writeError(w, http.StatusBadRequest, "duration_s must be between 10 and 600")
		return
	}
	// Load from a benchmark would become the idle baseline.
	if a.bjob.status().State == benchRunning {
		writeError(w, http.StatusConflict, "a benchmark is running; calibrate once it ends")
		return
	}
	j := a.cjob
	j.mu.Lock()
	if j.state.State == benchRunning {
		j.mu.Unlock()
		writeError(w, http.StatusConflict, "a calibration is already running")
		return
	}
	a.mu.RLock()
	chans := a.smp.Channels()
	a.mu.RUnlock()
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	now := time.Now().UTC()
	j.state = api.CalibrationJob{SchemaVersion: api.SchemaVersion, State: benchRunning, DurationS: d.Seconds(), StartedAt: &now}
	j.collector, j.cancel, j.done = calibrate.NewCollector(chans), cancel, make(chan struct{})
	j.mu.Unlock()
	go a.runCalibration(ctx, now, d, actor(r))
	a.publishCalibration()
	writeJSON(w, http.StatusAccepted, j.status())
}

func (a *Agent) handleCalibrationRun(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.cjob.status())
}

func (a *Agent) handleCancelCalibration(w http.ResponseWriter, _ *http.Request) {
	j := a.cjob
	j.mu.Lock()
	running, cancel := j.state.State == benchRunning, j.cancel
	j.mu.Unlock()
	if !running {
		writeError(w, http.StatusConflict, "no calibration is running")
		return
	}
	cancel()
	writeJSON(w, http.StatusAccepted, j.status())
}

// stopCalibration cancels a running calibration at shutdown and waits for
// it.
func (a *Agent) stopCalibration(ctx context.Context) {
	j := a.cjob
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
	}
}

func (a *Agent) runCalibration(ctx context.Context, start time.Time, d time.Duration, who string) {
	j := a.cjob
	finish := func(state string, res *calibrate.Result, err error) {
		j.mu.Lock()
		now := time.Now().UTC()
		j.state.State, j.state.EndedAt, j.state.Result = state, &now, res
		if err != nil {
			j.state.Error = err.Error()
		}
		j.collector, j.cancel = nil, nil
		close(j.done)
		j.mu.Unlock()
		a.publishCalibration()
	}
	select {
	case <-ctx.Done():
		finish(benchCanceled, nil, nil)
		return
	case <-time.After(d):
	}
	j.mu.Lock()
	c := j.collector
	j.collector = nil
	j.mu.Unlock()
	res, err := c.Result(start, d, a.hostFS)
	if err == nil {
		err = validCalibration(res)
	}
	if err != nil {
		finish(benchFailed, nil, err)
		return
	}
	if err := a.st.SaveCalibration(ctx, res); err != nil {
		a.log.Error("saving the calibration failed", "error", err)
		finish(benchFailed, nil, fmt.Errorf("saving the calibration failed"))
		return
	}
	if err := a.st.Audit(ctx, who, "calibrate", fmt.Sprintf("%d samples over %s", res.Samples, res.Duration)); err != nil {
		a.log.Warn("audit log write failed", "error", err)
	}
	a.mu.Lock()
	a.calib = &res
	a.mu.Unlock()
	a.log.Info("calibration applied", "actor", who, "baseline_cpu_w", res.Baseline().CPUW)
	finish(benchDone, &res, nil)
}
