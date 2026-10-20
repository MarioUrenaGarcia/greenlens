// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/attribution"
	"github.com/MarioUrenaGarcia/greenlens/internal/backends"
	"github.com/MarioUrenaGarcia/greenlens/internal/connector"
	"github.com/MarioUrenaGarcia/greenlens/internal/discovery"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/procfs"
	"github.com/MarioUrenaGarcia/greenlens/internal/sampler"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
	"github.com/MarioUrenaGarcia/greenlens/internal/usage"
)

// metricsFlag is the option that makes an engine serve its counters.
var metricsFlag = map[discovery.Kind]string{
	discovery.LlamaCpp: "--metrics",
	discovery.SGLang:   "--enable-metrics",
}

// backendState is what the API shows of one backend.
type backendState struct {
	info      backends.Backend
	present   bool
	processes int
	reading   backends.Reading
	read      bool
	total     usage.Energy
	powerW    float64
	models    map[string]*api.Model
}

// refreshBackends finds the backends and their processes again.
func (a *Agent) refreshBackends(ctx context.Context) {
	snap, err := procfs.TakeSnapshot(a.hostFS)
	if err != nil {
		a.limited("processes", "reading the processes failed", "error", err)
		return
	}
	a.mu.RLock()
	static, discover := a.cfg.Backends, a.cfg.Discovery.Enabled
	a.mu.RUnlock()
	bs := backends.Resolve(a.hostFS, snap, static, discover)
	a.tracker.Refresh(snap, backends.Roots(bs))
	a.drm.Scan(a.tracker.PIDs())
	groups := a.tracker.Groups()

	var infos []store.BackendInfo
	a.bmu.Lock()
	seen := map[string]bool{}
	for _, b := range bs {
		seen[b.Name] = true
		st, ok := a.bstate[b.Name]
		if !ok {
			st = &backendState{models: map[string]*api.Model{}}
			a.bstate[b.Name] = st
		}
		if !st.present && groups[b.Name] > 0 {
			a.log.Info("backend found", "backend", b.Name, "kind", b.Kind, "endpoint", b.Endpoint, "static", b.Static, "processes", groups[b.Name])
		}
		st.info, st.present, st.processes = b, groups[b.Name] > 0 || b.Static, groups[b.Name]
		infos = append(infos, store.BackendInfo{Name: b.Name, Kind: string(b.Kind), Endpoint: b.Endpoint})
	}
	for name, st := range a.bstate {
		if !seen[name] && st.present {
			a.log.Info("backend gone", "backend", name)
			st.present, st.processes = false, 0
		}
	}
	a.bs = bs
	a.bmu.Unlock()
	if err := a.st.EnsureBackends(ctx, infos, time.Now()); err != nil {
		a.limited("backends", "recording the backends failed", "error", err)
	}
}

// accountBackends adds the share of every backend in one sample.
func (a *Agent) accountBackends(s sampler.Sample, busy uint64) {
	ticks := a.tracker.Sample()
	a.mu.RLock()
	base := attribution.Baseline{}
	if a.calib != nil {
		base = a.calib.Baseline()
	}
	chans := a.smp.Channels()
	quality := a.pkgQuality
	users := a.gpuUsers
	a.mu.RUnlock()

	cpuJ, _ := s.DomainEnergy(source.DomainPackage)
	dramJ, _ := s.DomainEnergy(source.DomainDRAM)
	node := attribution.Node{Interval: s.Interval, CPUJ: cpuJ, DRAMJ: dramJ, Idle: base, BusyTicks: busy}
	for _, d := range []source.Domain{source.DomainWall, source.DomainPlatform} {
		if e, ok := s.DomainEnergy(d); ok {
			node.PlatformJ, node.HasPlatform = e, true
			break
		}
	}

	// The DRM statistics of the backends cover the GPUs whose source
	// reports no processes, as hwmon does for amdgpu, xe and i915.
	use := a.drm.Use()
	for _, u := range users {
		for inst, g := range u.GPUUse() {
			use[inst] = g
		}
	}
	var gpus []usage.GPU
	for _, c := range s.Channels {
		if c.Domain != source.DomainGPU {
			continue
		}
		ch := chans[c.Channel]
		g := usage.GPU{GPU: attribution.GPU{Interval: s.Interval, EnergyJ: c.EnergyJ, IdleW: base.GPUW[ch.Instance], Busy: map[string]float64{}}, Quality: ch.Quality}
		u := use[ch.Instance]
		for pid, b := range u.Busy {
			if grp, ok := a.tracker.GroupOf(pid); ok {
				g.Busy[grp] += b
			} else {
				g.Other += b
			}
		}
		for _, pid := range u.PIDs {
			if grp, ok := a.tracker.GroupOf(pid); ok {
				g.Holders = append(g.Holders, grp)
			}
		}
		gpus = append(gpus, g)
	}
	a.acc.Add(usage.Sample{Node: node, Ticks: ticks, GPUs: gpus, Quality: quality})
}

// cutUsage closes a usage interval and hands it to the worker, which reads
// the backends without holding up the sampling.
func (a *Agent) cutUsage(now time.Time) {
	c := a.acc.Cut(now)
	select {
	case a.cuts <- c:
	default:
		a.limited("usage-queue", "a usage interval was dropped because the previous ones are still being stored")
	}
}

// usageWorker reads the counters of the backends at the end of every
// interval, splits the energy between models and stores the rows.
func (a *Agent) usageWorker(ctx context.Context) {
	defer close(a.workerDone)
	for c := range a.cuts {
		a.mu.RLock()
		maxModels := a.cfg.Attribution.MaxModelsPerBackend
		a.mu.RUnlock()
		a.bmu.Lock()
		bs := slices.Clone(a.bs)
		a.bmu.Unlock()
		readings := a.reader.Read(ctx, bs)
		a.split.SetMaxModels(maxModels)
		rows := a.split.Split(c, readings)
		info := map[string]store.BackendInfo{}
		for _, b := range bs {
			info[b.Name] = store.BackendInfo{Name: b.Name, Kind: string(b.Kind), Endpoint: b.Endpoint}
		}
		if err := a.st.InsertUsage(ctx, rows, info, time.Now()); err != nil {
			a.limited("usage-insert", "storing usage failed", "error", err)
		}
		a.recordUsage(c, readings, rows)
		every := regressionEvery
		if a.o.RegressionEvery > 0 {
			every = a.o.RegressionEvery
		}
		if time.Since(a.lastRegress) >= every {
			a.lastRegress = time.Now()
			a.regress(ctx)
		}
	}
}

// regressionEvery is how often the cost of prompt and generated tokens is
// fitted again; regressionWindow is the period each fit covers.
const (
	regressionEvery  = 10 * time.Minute
	regressionWindow = 24 * time.Hour
)

// regress fits the energy of each model on its tokens.
func (a *Agent) regress(ctx context.Context) {
	a.mu.RLock()
	minR2 := a.cfg.Attribution.RegressionMinR2
	a.mu.RUnlock()
	type key struct{ backend, model string }
	var todo []key
	a.bmu.Lock()
	for name, st := range a.bstate {
		for m, mm := range st.models {
			if mm.GeneratedTokens > 0 {
				todo = append(todo, key{name, m})
			}
		}
	}
	a.bmu.Unlock()
	now := time.Now()
	fits := map[key]*usage.Regression{}
	for _, k := range todo {
		its, err := a.st.ModelIntervals(ctx, k.backend, k.model, now.Add(-regressionWindow), now)
		if err != nil {
			a.limited("regression", "reading usage for the regression failed", "error", err)
			return
		}
		obs := make([]usage.Observation, len(its))
		for i, it := range its {
			obs[i] = usage.Observation{EnergyJ: it.EnergyJ, PromptTokens: it.PromptTokens, GeneratedTokens: it.GeneratedTokens}
		}
		if r, err := usage.Regress(obs, minR2); err == nil {
			fits[k] = &r
		}
	}
	a.bmu.Lock()
	defer a.bmu.Unlock()
	for _, k := range todo {
		if st, ok := a.bstate[k.backend]; ok {
			if m, ok := st.models[k.model]; ok {
				m.Regression = fits[k]
			}
		}
	}
}

func (a *Agent) recordUsage(c usage.Cut, readings map[string]backends.Reading, rows []usage.Row) {
	a.bmu.Lock()
	defer a.bmu.Unlock()
	for name, rd := range readings {
		if st, ok := a.bstate[name]; ok {
			st.reading, st.read = rd, true
			if rd.Err != nil {
				a.limited("backend:"+name, "reading a backend failed", "backend", name, "error", rd.Err)
			}
		}
	}
	interval := c.End.Sub(c.Start).Seconds()
	power := map[string]float64{}
	for _, r := range rows {
		st, ok := a.bstate[r.Backend]
		if !ok {
			st = &backendState{info: backends.Backend{Name: r.Backend, Kind: discovery.Process}, models: map[string]*api.Model{}}
			a.bstate[r.Backend] = st
		}
		st.total.CPUJ += r.CPUJ
		st.total.DRAMJ += r.DRAMJ
		st.total.GPUJ += r.GPUJ
		st.total.PlatformJ += r.PlatformJ
		if interval > 0 {
			power[r.Backend] += r.TotalJ() / interval
		}
		if r.Model == "" {
			continue
		}
		m, ok := st.models[r.Model]
		if !ok {
			m = &api.Model{Name: r.Model}
			st.models[r.Model] = m
		}
		m.EnergyJ += r.TotalJ()
		for _, t := range []struct {
			dst *int64
			src *int64
		}{{&m.PromptTokens, r.PromptTokens}, {&m.GeneratedTokens, r.GeneratedTokens}, {&m.Requests, r.Requests}} {
			if t.src != nil {
				*t.dst += *t.src
			}
		}
		m.Method, m.Quality = r.Method, string(r.Quality)
	}
	for _, st := range a.bstate {
		st.powerW = power[st.info.Name]
	}
}

// backendProblem turns a failed reading into a coded problem.
func backendProblem(b backends.Backend, err error) *api.Problem {
	var p api.Problem
	if flag, ok := metricsFlag[b.Kind]; ok && errors.Is(err, connector.ErrNoMetrics) {
		p = api.FromProblem(source.Problem{Code: help.GL2002, Args: []any{"name", b.Name, "kind", string(b.Kind), "flag", flag}})
	} else {
		p = api.FromProblem(source.Problem{Code: help.GL2001, Args: []any{"name", b.Name, "endpoint", b.Endpoint, "detail", err.Error()}})
	}
	p.Backend = b.Name
	return &p
}

// Backends assembles the backends document.
func (a *Agent) Backends() api.Backends {
	a.mu.RLock()
	interval := usage.Interval.Milliseconds()
	a.mu.RUnlock()
	a.bmu.Lock()
	defer a.bmu.Unlock()
	out := api.Backends{SchemaVersion: api.SchemaVersion, IntervalMS: interval, Backends: []api.Backend{}}
	for _, st := range a.bstate {
		b := api.Backend{
			Name: st.info.Name, Kind: string(st.info.Kind), Endpoint: st.info.Endpoint, Static: st.info.Static,
			ContainerID: st.info.ContainerID, Processes: st.processes,
			PowerW: st.powerW, EnergyJ: st.total.TotalJ(), CPUJ: st.total.CPUJ, DRAMJ: st.total.DRAMJ, GPUJ: st.total.GPUJ,
		}
		if st.read {
			b.LastRead = st.reading.At
			if st.reading.Err != nil {
				b.Problem = backendProblem(st.info, st.reading.Err)
			} else {
				b.Version = st.reading.Snapshot.Version
				b.TokenCounters = st.reading.Snapshot.TokenCounters
			}
		}
		for _, m := range st.models {
			mm := *m
			if mm.GeneratedTokens > 0 {
				mm.JoulesPerToken = mm.EnergyJ / float64(mm.GeneratedTokens)
			}
			b.Models = append(b.Models, mm)
		}
		slices.SortFunc(b.Models, func(x, y api.Model) int {
			switch {
			case x.EnergyJ > y.EnergyJ:
				return -1
			case x.EnergyJ < y.EnergyJ:
				return 1
			}
			return cmp.Compare(x.Name, y.Name)
		})
		out.Backends = append(out.Backends, b)
	}
	slices.SortFunc(out.Backends, func(x, y api.Backend) int { return cmp.Compare(x.Name, y.Name) })
	return out
}
