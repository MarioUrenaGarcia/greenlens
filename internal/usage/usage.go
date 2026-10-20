// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package usage turns the energy of the node into energy per backend and
// per model. Every sample adds to each backend its share of the processor
// and memory energy, by CPU time, and of the energy of the GPUs it runs on.
// At the end of each interval the energy of a backend is split between its
// models by the tokens they processed.
package usage

import (
	"math"
	"slices"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/attribution"
	"github.com/MarioUrenaGarcia/greenlens/internal/backends"
	"github.com/MarioUrenaGarcia/greenlens/internal/connector"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

// Interval is how often usage rows are written. Ten seconds keeps the
// database small while leaving enough intervals per hour for the prefill
// and decode regression.
const Interval = 10 * time.Second

// Methods of usage rows, from the most to the least direct.
const (
	MethodCPUShare      = attribution.Method
	MethodGPUProcess    = attribution.MethodGPUProcess
	MethodTokenWeighted = "token_weighted"
	MethodEstimated     = attribution.MethodEstimated
)

var methodRank = map[string]int{MethodCPUShare: 0, MethodGPUProcess: 1, MethodTokenWeighted: 2, MethodEstimated: 3}

func weaker(a, b string) string {
	if a == "" || methodRank[b] > methodRank[a] {
		return b
	}
	return a
}

var qualityRank = map[source.Quality]int{source.QualityMeasured: 0, source.QualityPlatform: 1, source.QualityEstimated: 2}

func worse(a, b source.Quality) source.Quality {
	if a == "" || qualityRank[b] > qualityRank[a] {
		return b
	}
	return a
}

// GPU is the energy of one GPU in a sample with the backends that used it.
type GPU struct {
	attribution.GPU
	Quality source.Quality
}

// Sample is what one sample contributes.
type Sample struct {
	// Node holds the processor and memory energy and the busy time of the
	// system.
	Node attribution.Node
	// Ticks is the CPU time of each backend.
	Ticks map[string]uint64
	GPUs  []GPU
	// Quality is that of the processor energy.
	Quality source.Quality
}

// Energy is what one backend used in an interval.
type Energy struct {
	CPUJ      float64
	DRAMJ     float64
	GPUJ      float64
	PlatformJ float64
	Method    string
	Quality   source.Quality
}

// TotalJ is the energy of the backend: the platform share under the wall
// policy, otherwise processor, memory and GPU.
func (e Energy) TotalJ() float64 {
	if e.PlatformJ > 0 {
		return e.PlatformJ
	}
	return e.CPUJ + e.DRAMJ + e.GPUJ
}

func (e Energy) scaled(f float64) Energy {
	e.CPUJ *= f
	e.DRAMJ *= f
	e.GPUJ *= f
	e.PlatformJ *= f
	return e
}

// Cut is the energy of every backend over one interval.
type Cut struct {
	Start    time.Time
	End      time.Time
	Backends map[string]Energy
	// Quality is that of the processor energy in the interval, for rows
	// with tokens and no energy.
	Quality source.Quality
}

// Accountant adds up the energy of the backends between cuts. It is not
// safe for concurrent use.
type Accountant struct {
	policy  attribution.Policy
	start   time.Time
	energy  map[string]Energy
	quality source.Quality
}

// NewAccountant starts accounting at start.
func NewAccountant(policy attribution.Policy, start time.Time) *Accountant {
	return &Accountant{policy: policy, start: start, energy: map[string]Energy{}}
}

// SetPolicy changes the idle policy from the next sample.
func (a *Accountant) SetPolicy(p attribution.Policy) { a.policy = p }

// Add accounts one sample.
func (a *Accountant) Add(s Sample) {
	a.quality = worse(a.quality, s.Quality)
	use := make([]attribution.Usage, 0, len(s.Ticks))
	for name, t := range s.Ticks {
		use = append(use, attribution.Usage{ID: name, Ticks: t})
	}
	for _, r := range attribution.Split(s.Node, use, a.policy) {
		if r.CPUJ == 0 && r.DRAMJ == 0 && r.PlatformJ == 0 {
			continue
		}
		e := a.energy[r.ID]
		e.CPUJ += r.CPUJ
		e.DRAMJ += r.DRAMJ
		e.PlatformJ += r.PlatformJ
		e.Method = weaker(e.Method, MethodCPUShare)
		q := s.Quality
		if r.PlatformJ > 0 {
			q = worse(q, source.QualityPlatform)
		}
		e.Quality = worse(e.Quality, q)
		a.energy[r.ID] = e
	}
	for _, g := range s.GPUs {
		split, method := attribution.SplitGPU(g.GPU, a.policy)
		for name, j := range split {
			if j <= 0 {
				continue
			}
			e := a.energy[name]
			e.GPUJ += j
			e.Method = weaker(e.Method, method)
			e.Quality = worse(e.Quality, g.Quality)
			a.energy[name] = e
		}
	}
}

// Cut returns the energy since the previous cut and starts a new interval
// at end.
func (a *Accountant) Cut(end time.Time) Cut {
	c := Cut{Start: a.start, End: end, Backends: a.energy, Quality: a.quality}
	if c.Quality == "" {
		c.Quality = source.QualityMeasured
	}
	a.start, a.energy, a.quality = end, map[string]Energy{}, ""
	return c
}

// Row is one line of usage: the energy and tokens of one model of one
// backend in one interval. Model is empty when the model is not known.
type Row struct {
	Start    time.Time
	Interval time.Duration
	Backend  string
	Model    string
	Energy
	// Tokens are nil when the backend does not count them.
	PromptTokens    *int64
	GeneratedTokens *int64
	Requests        *int64
}

// Other names the models beyond the limit of a backend.
const Other = "other"

// Splitter splits the energy of each backend between its models. It keeps
// the counters of the previous interval, so it must see every interval in
// order. It is not safe for concurrent use.
type Splitter struct {
	maxModels int
	prev      map[string]map[string]connector.Counters
	models    map[string][]string
}

// NewSplitter tracks at most maxModels models per backend.
func NewSplitter(maxModels int) *Splitter {
	return &Splitter{maxModels: maxModels, prev: map[string]map[string]connector.Counters{}, models: map[string][]string{}}
}

// SetMaxModels changes the limit for models not seen yet.
func (s *Splitter) SetMaxModels(n int) { s.maxModels = n }

// name maps a model to the name it is tracked under.
func (s *Splitter) name(backend, model string) string {
	known := s.models[backend]
	if slices.Contains(known, model) {
		return model
	}
	if len(known) >= s.maxModels {
		return Other
	}
	s.models[backend] = append(known, model)
	return model
}

func diff(cur, prev float64) float64 {
	if cur < prev {
		// The backend restarted and its counters started over.
		return cur
	}
	return cur - prev
}

func count(v float64) *int64 {
	n := int64(math.Round(v))
	return &n
}

// Split returns the rows of an interval. readings are the counters read at
// its end; a backend without a reading keeps its previous counters, so the
// tokens of a missed reading go to the next interval.
func (s *Splitter) Split(c Cut, readings map[string]backends.Reading) []Row {
	names := make([]string, 0, len(c.Backends)+len(readings))
	for name := range c.Backends {
		names = append(names, name)
	}
	for name := range readings {
		if _, ok := c.Backends[name]; !ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	var out []Row
	for _, b := range names {
		out = append(out, s.backend(c, b, c.Backends[b], readings[b])...)
	}
	return out
}

func (s *Splitter) backend(c Cut, b string, e Energy, rd backends.Reading) []Row {
	base := Row{Start: c.Start, Interval: c.End.Sub(c.Start), Backend: b}
	if e.Method == "" {
		e.Method, e.Quality = MethodCPUShare, c.Quality
	}
	counted := rd.Err == nil && rd.Snapshot.TokenCounters
	var deltas []connector.Counters
	if counted {
		cur := map[string]connector.Counters{}
		for _, m := range rd.Snapshot.Models {
			n := s.name(b, m.Model)
			k := cur[n]
			k.Model = n
			k.PromptTokens += m.PromptTokens
			k.GeneratedTokens += m.GeneratedTokens
			k.Requests += m.Requests
			cur[n] = k
		}
		prev, seen := s.prev[b]
		s.prev[b] = cur
		if !seen {
			// The first reading is the starting point of the counters.
			counted = false
			cur = nil
		}
		for n, k := range cur {
			p := prev[n]
			d := connector.Counters{Model: n, PromptTokens: diff(k.PromptTokens, p.PromptTokens), GeneratedTokens: diff(k.GeneratedTokens, p.GeneratedTokens), Requests: diff(k.Requests, p.Requests)}
			if d.PromptTokens+d.GeneratedTokens+d.Requests > 0 {
				deltas = append(deltas, d)
			}
		}
		slices.SortFunc(deltas, func(x, y connector.Counters) int {
			if x.Model < y.Model {
				return -1
			}
			if x.Model > y.Model {
				return 1
			}
			return 0
		})
	}

	if len(deltas) == 0 {
		if e.TotalJ() == 0 {
			return nil
		}
		r := base
		r.Energy = e
		r.Model = s.only(b, rd)
		if counted {
			zero := int64(0)
			r.PromptTokens, r.GeneratedTokens, r.Requests = &zero, &zero, &zero
		}
		return []Row{r}
	}

	weight := func(d connector.Counters) float64 { return d.PromptTokens + d.GeneratedTokens }
	total := 0.0
	for _, d := range deltas {
		total += weight(d)
	}
	if total == 0 {
		// Requests without token counts, as in a backend that only counts
		// requests, weigh by request.
		weight = func(d connector.Counters) float64 { return d.Requests }
		for _, d := range deltas {
			total += weight(d)
		}
	}
	out := make([]Row, 0, len(deltas))
	for _, d := range deltas {
		r := base
		r.Model = d.Model
		r.Energy = e.scaled(weight(d) / total)
		if len(deltas) > 1 && e.TotalJ() > 0 {
			r.Method = weaker(e.Method, MethodTokenWeighted)
		}
		r.PromptTokens, r.GeneratedTokens, r.Requests = count(d.PromptTokens), count(d.GeneratedTokens), count(d.Requests)
		out = append(out, r)
	}
	return out
}

// only returns the model of a backend that serves a single one, or "".
func (s *Splitter) only(b string, rd backends.Reading) string {
	if rd.Err != nil {
		return ""
	}
	switch {
	case len(rd.Snapshot.Loaded) == 1:
		return s.name(b, rd.Snapshot.Loaded[0])
	case len(rd.Snapshot.Loaded) == 0 && len(rd.Snapshot.Models) == 1:
		return s.name(b, rd.Snapshot.Models[0].Model)
	}
	return ""
}
