// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package report builds the reports of a period from the energy and the
// indicators the agent computed: the Software Carbon Intensity of the node
// and of each backend, model or consumer, and the environmental report of
// the node.
package report

import (
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

// SchemaVersion is the version of the report documents.
const SchemaVersion = 1

// EmbodiedParams are the embodied carbon parameters in force.
type EmbodiedParams struct {
	ServerKgCO2e  *float64 `json:"server_kgco2e,omitempty"`
	GPUKgCO2e     *float64 `json:"gpu_kgco2e,omitempty"`
	LifetimeYears *float64 `json:"lifetime_years,omitempty"`
}

// SCIRow is the carbon intensity of the node or of one group.
type SCIRow struct {
	Backend  string `json:"backend,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Model    string `json:"model,omitempty"`
	Consumer string `json:"consumer,omitempty"`
	// EnergyJ is the energy of the processor, memory and GPU; ServerEnergyJ
	// and GPUEnergyJ split it.
	EnergyJ       float64 `json:"energy_j"`
	ServerEnergyJ float64 `json:"server_energy_j"`
	GPUEnergyJ    float64 `json:"gpu_energy_j"`
	// ServerShare and GPUShare are the parts of the hardware that carry
	// its embodied carbon.
	ServerShare     float64           `json:"server_share"`
	GPUShare        float64           `json:"gpu_share"`
	GeneratedTokens int64             `json:"generated_tokens"`
	Requests        int64             `json:"requests"`
	Consumers       int64             `json:"consumers"`
	Indicators      indicators.Values `json:"indicators"`
	SCI             indicators.SCI    `json:"sci"`
}

// SCIReport is the Software Carbon Intensity of a period.
type SCIReport struct {
	SchemaVersion int            `json:"schema_version"`
	From          time.Time      `json:"from"`
	To            time.Time      `json:"to"`
	GroupBy       string         `json:"group_by,omitempty"`
	Unit          string         `json:"functional_unit"`
	Factors       api.Factors    `json:"factors"`
	Embodied      EmbodiedParams `json:"embodied"`
	// Missing names the configuration that the embodied carbon lacks.
	Missing []string `json:"missing"`
	Node    SCIRow   `json:"node"`
	Rows    []SCIRow `json:"rows"`
}

// Node is the energy of the node in the period, with what the backends
// counted on it.
type Node struct {
	// ServerJ is the energy of the processor and memory, GPUJ that of the
	// GPUs, and Indicators those of both.
	ServerJ, GPUJ   float64
	Indicators      indicators.Values
	GeneratedTokens int64
	Requests        int64
	Consumers       int64
}

// SCIInput is what the SCI report of a period is built from.
type SCIInput struct {
	From, To time.Time
	GroupBy  string
	Unit     string
	Factors  api.Factors
	Embodied indicators.Embodied
	Node     Node
	// Groups are the usage rows of the grouping, with their indicators;
	// Consumers counts the consumers of each backend, for the unit
	// consumer.
	Groups    []store.UsageEnergy
	Consumers map[string]int64
}

// BuildSCI computes the SCI of the node, which carries the whole hardware,
// and of each group, which carries the share of the server and of the GPU
// that its energy is of the energy of the node.
func BuildSCI(in SCIInput) SCIReport {
	period := in.To.Sub(in.From)
	e := in.Embodied
	rep := SCIReport{
		SchemaVersion: SchemaVersion, From: in.From, To: in.To, GroupBy: in.GroupBy, Unit: in.Unit, Factors: in.Factors,
		Embodied: EmbodiedParams{ServerKgCO2e: e.ServerKg, GPUKgCO2e: e.GPUKg, LifetimeYears: e.LifetimeYears},
		Missing:  e.Missing(), Rows: []SCIRow{},
	}
	if rep.Missing == nil {
		rep.Missing = []string{}
	}
	n := in.Node
	rep.Node = SCIRow{
		EnergyJ: n.ServerJ + n.GPUJ, ServerEnergyJ: n.ServerJ, GPUEnergyJ: n.GPUJ, ServerShare: 1, GPUShare: 1,
		GeneratedTokens: n.GeneratedTokens, Requests: n.Requests, Consumers: n.Consumers, Indicators: n.Indicators,
	}
	rep.Node.SCI = indicators.ComputeSCI(in.Unit, indicators.UnitsOf(in.Unit, n.GeneratedTokens, n.Requests, n.Consumers),
		n.Indicators.CO2eG, e.Grams(period, 1, 1))
	for _, g := range in.Groups {
		row := SCIRow{
			Backend: g.Backend, Kind: g.Kind, Model: g.Model, Consumer: g.Consumer,
			EnergyJ: g.EnergyJ, GPUEnergyJ: g.GPUEnergyJ, ServerEnergyJ: max(0, g.EnergyJ-g.GPUEnergyJ),
			GeneratedTokens: g.GeneratedTokens, Requests: g.Requests,
		}
		switch {
		case in.GroupBy == store.ByConsumer && g.Consumer != "":
			row.Consumers = 1
		case in.GroupBy == store.ByBackend:
			row.Consumers = in.Consumers[g.Backend]
		}
		if g.Indicators != nil {
			row.Indicators = *g.Indicators
		}
		row.ServerShare = indicators.Share(row.ServerEnergyJ, n.ServerJ)
		row.GPUShare = indicators.Share(row.GPUEnergyJ, n.GPUJ)
		row.SCI = indicators.ComputeSCI(in.Unit, indicators.UnitsOf(in.Unit, row.GeneratedTokens, row.Requests, row.Consumers),
			row.Indicators.CO2eG, e.Grams(period, row.ServerShare, row.GPUShare))
		rep.Rows = append(rep.Rows, row)
	}
	return rep
}
