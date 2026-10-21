// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"net/http"
	"runtime"
	"slices"
	"strings"

	"github.com/MarioUrenaGarcia/greenlens/internal/exporter/prometheus"
	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/usage"
	"github.com/MarioUrenaGarcia/greenlens/internal/version"
)

// Emissions are operational for now: the energy used times the grid factor.
const (
	scopeOperational = "operational"
	scopeEmbodied    = "embodied"
)

func (a *Agent) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", prometheus.ContentType)
	if err := prometheus.Write(w, a.metrics()); err != nil {
		a.limited("metrics", "writing the metrics failed", "error", err)
	}
}

type nodeKey struct {
	domain, source, quality string
}

// metrics assembles every family. Channels of the same domain, source and
// quality, such as the packages of two sockets, are added together so each
// series has a single value.
func (a *Agent) metrics() []prometheus.Family {
	L := prometheus.L
	a.mu.RLock()
	energyJ, powerW := map[nodeKey]float64{}, map[nodeKey]float64{}
	var keys []nodeKey
	for _, ch := range a.sel.Channels {
		k := nodeKey{string(ch.Domain), ch.Source, string(ch.Quality)}
		if _, seen := energyJ[k]; !seen {
			keys = append(keys, k)
		}
		energyJ[k] += a.chanJ[ch.ID]
		powerW[k] += a.chanW[ch.ID]
	}
	selected := a.sel.Sources()
	var sources []string
	for _, c := range a.cands {
		if id := c.Source.ID(); !slices.Contains(sources, id) {
			sources = append(sources, id)
		}
	}
	owner := map[string]string{}
	for _, ch := range a.sel.Channels {
		owner[ch.ID] = ch.Source
	}
	reading := map[string]bool{}
	for _, c := range a.last.Channels {
		reading[owner[c.Channel]] = true
	}
	calib := a.calib
	selfJ, unattributedJ := a.selfJ, a.unattributedJ
	currency := a.cfg.Indicators.Tariff.Currency
	unit := a.cfg.Indicators.SCI.FunctionalUnit
	a.mu.RUnlock()

	slices.SortFunc(keys, func(x, y nodeKey) int {
		return strings.Compare(x.domain+x.source+x.quality, y.domain+y.source+y.quality)
	})
	nodeEnergy := prometheus.Family{Name: "greenlens_node_energy_joules_total", Help: "Energy measured or estimated for each domain since the agent started.", Type: prometheus.Counter}
	nodePower := prometheus.Family{Name: "greenlens_node_power_watts", Help: "Power of each domain in the latest sample.", Type: prometheus.Gauge}
	for _, k := range keys {
		l := L("domain", k.domain, "source", k.source, "quality", k.quality)
		nodeEnergy.Samples = append(nodeEnergy.Samples, prometheus.Sample{Labels: l, Value: energyJ[k]})
		nodePower.Samples = append(nodePower.Samples, prometheus.Sample{Labels: l, Value: powerW[k]})
	}
	idle := prometheus.Family{Name: "greenlens_node_idle_power_watts", Help: "Idle power of each domain measured by the latest calibration.", Type: prometheus.Gauge}
	if calib != nil {
		b := calib.Baseline()
		gpu := 0.0
		for _, w := range b.GPUW {
			gpu += w
		}
		for _, d := range []struct {
			domain source.Domain
			w      float64
		}{{source.DomainPackage, b.CPUW}, {source.DomainDRAM, b.DRAMW}, {source.DomainGPU, gpu}, {source.DomainPlatform, b.PlatformW}} {
			if d.w > 0 {
				idle.Samples = append(idle.Samples, prometheus.Sample{Labels: L("domain", string(d.domain)), Value: d.w})
			}
		}
	}
	unattributed := prometheus.Family{Name: "greenlens_node_unattributed_energy_joules_total", Help: "Energy of the node that no backend received.", Type: prometheus.Counter,
		Samples: []prometheus.Sample{{Value: unattributedJ}}}

	up := prometheus.Family{Name: "greenlens_source_up", Help: "Whether an energy source is selected and returned values in the latest sample.", Type: prometheus.Gauge}
	slices.Sort(sources)
	for _, s := range sources {
		v := 0.0
		if slices.Contains(selected, s) && reading[s] {
			v = 1
		}
		up.Samples = append(up.Samples, prometheus.Sample{Labels: L("source", s), Value: v})
	}

	backendEnergy := prometheus.Family{Name: "greenlens_backend_energy_joules_total", Help: "Energy attributed to each backend since the agent started, by component.", Type: prometheus.Counter}
	modelEnergy := prometheus.Family{Name: "greenlens_model_energy_joules_total", Help: "Energy attributed to each model since the agent started.", Type: prometheus.Counter}
	modelTokens := prometheus.Family{Name: "greenlens_model_tokens_total", Help: "Tokens counted for each model since the agent started.", Type: prometheus.Counter}
	jpt := prometheus.Family{Name: "greenlens_model_joules_per_token", Help: "Energy per generated token of each model.", Type: prometheus.Gauge}
	emissions := prometheus.Family{Name: "greenlens_emissions_grams_co2e_total", Help: "Emissions of the energy of each model, in grams of CO2 equivalent.", Type: prometheus.Counter}
	cost := prometheus.Family{Name: "greenlens_energy_cost_total", Help: "Cost of the energy of each model, in the configured currency.", Type: prometheus.Counter}
	water := prometheus.Family{Name: "greenlens_water_liters_total", Help: "Water used for the energy of each model.", Type: prometheus.Counter}
	sci := prometheus.Family{Name: "greenlens_sci_grams_co2e_per_unit", Help: "Software Carbon Intensity of each model since the agent started: operational and embodied emissions per functional unit.", Type: prometheus.Gauge}
	consumers := prometheus.Family{Name: "greenlens_consumer_energy_joules_total", Help: "Energy attributed to each consumer identified by the proxy.", Type: prometheus.Counter}

	a.bmu.Lock()
	names := make([]string, 0, len(a.bstate))
	for n := range a.bstate {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		st := a.bstate[n]
		kind, method := string(st.info.Kind), st.method
		if method == "" {
			method = usage.MethodCPUShare
		}
		for _, c := range []struct {
			name string
			j    float64
		}{{"cpu", st.total.CPUJ}, {"dram", st.total.DRAMJ}, {"gpu", st.total.GPUJ}, {"platform", st.total.PlatformJ}} {
			if c.j > 0 {
				backendEnergy.Samples = append(backendEnergy.Samples, prometheus.Sample{Labels: L("backend", n, "kind", kind, "component", c.name, "method", method), Value: c.j})
			}
		}
		models := make([]string, 0, len(st.models))
		for m := range st.models {
			models = append(models, m)
		}
		slices.Sort(models)
		for _, mn := range models {
			m := st.models[mn]
			modelEnergy.Samples = append(modelEnergy.Samples, prometheus.Sample{Labels: L("backend", n, "model", mn, "method", m.Method), Value: m.EnergyJ})
			modelTokens.Samples = append(modelTokens.Samples,
				prometheus.Sample{Labels: L("backend", n, "model", mn, "type", "prompt"), Value: float64(m.PromptTokens)},
				prometheus.Sample{Labels: L("backend", n, "model", mn, "type", "generated"), Value: float64(m.GeneratedTokens)})
			if m.GeneratedTokens > 0 {
				jpt.Samples = append(jpt.Samples, prometheus.Sample{Labels: L("backend", n, "model", mn, "window", "since_start"), Value: m.EnergyJ / float64(m.GeneratedTokens)})
			}
			embodied, hasEmbodied := st.embodiedG[mn]
			if hasEmbodied {
				emissions.Samples = append(emissions.Samples, prometheus.Sample{Labels: L("backend", n, "model", mn, "scope", scopeEmbodied), Value: embodied})
			}
			if mi := st.ind[mn]; mi != nil {
				emissions.Samples = append(emissions.Samples, prometheus.Sample{Labels: L("backend", n, "model", mn, "scope", scopeOperational), Value: mi.CO2eG})
				if units := indicators.UnitsOf(unit, m.GeneratedTokens, m.Requests, 0); units > 0 {
					sci.Samples = append(sci.Samples, prometheus.Sample{Labels: L("backend", n, "model", mn, "unit", unit), Value: (mi.CO2eG + embodied) / units})
				}
				if mi.Cost != nil {
					cost.Samples = append(cost.Samples, prometheus.Sample{Labels: L("backend", n, "model", mn, "currency", currency), Value: *mi.Cost})
				}
				if mi.WaterL != nil {
					water.Samples = append(water.Samples, prometheus.Sample{Labels: L("backend", n, "model", mn), Value: *mi.WaterL})
				}
			}
		}
	}
	cnames := make([]string, 0, len(a.consumerJ))
	for c := range a.consumerJ {
		cnames = append(cnames, c)
	}
	slices.Sort(cnames)
	for _, c := range cnames {
		consumers.Samples = append(consumers.Samples, prometheus.Sample{Labels: L("consumer", c), Value: a.consumerJ[c]})
	}
	a.bmu.Unlock()

	info := version.Get()
	build := prometheus.Family{Name: "greenlens_build_info", Help: "Version of the running agent.", Type: prometheus.Gauge,
		Samples: []prometheus.Sample{{Labels: L("version", a.o.Version, "revision", info.Revision, "goversion", runtime.Version()), Value: 1}}}
	self := prometheus.Family{Name: "greenlens_self_energy_joules_total", Help: "Energy attributed to the agent itself.", Type: prometheus.Counter,
		Samples: []prometheus.Sample{{Value: selfJ}}}

	fams := []prometheus.Family{nodeEnergy, nodePower, idle, unattributed, backendEnergy, modelEnergy, modelTokens, jpt, consumers, emissions, sci, cost, water, up}
	fams = append(fams, a.collectors.Families()...)
	return append(fams, self, build)
}
