// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package report

import (
	"cmp"
	"slices"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

// The environmental report follows the location-based method of Scope 2 of
// the GHG Protocol: the electricity of the processor, memory and GPU, with
// the overhead of the facility, at the intensity of the grid. The
// market-based method needs the factors of the supplier and is not
// reported.

// ESGDomain is the energy of one domain of the node.
type ESGDomain struct {
	Domain  source.Domain `json:"domain"`
	EnergyJ float64       `json:"energy_j"`
	// Component is false for domains that measure the components again,
	// such as platform and wall meters, which are shown but not added.
	Component bool   `json:"component"`
	Quality   string `json:"quality"`
}

// ESGGroup is the use of one backend or model.
type ESGGroup struct {
	Backend         string            `json:"backend"`
	Kind            string            `json:"kind"`
	Model           string            `json:"model,omitempty"`
	EnergyJ         float64           `json:"energy_j"`
	GeneratedTokens int64             `json:"generated_tokens"`
	Requests        int64             `json:"requests"`
	JPerToken       float64           `json:"j_per_generated_token,omitempty"`
	Indicators      indicators.Values `json:"indicators"`
}

// ESGReport is the environmental report of a period.
type ESGReport struct {
	SchemaVersion int         `json:"schema_version"`
	From          time.Time   `json:"from"`
	To            time.Time   `json:"to"`
	GeneratedAt   time.Time   `json:"generated_at"`
	Version       string      `json:"version"`
	Method        string      `json:"method"`
	Factors       api.Factors `json:"factors"`
	Domains       []ESGDomain `json:"domains"`
	// ComponentsJ is the electricity of the processor, memory and GPU, and
	// FacilityKWh the same with the overhead of the facility (PUE).
	ComponentsJ float64 `json:"components_j"`
	FacilityKWh float64 `json:"facility_kwh"`
	// Indicators hold the Scope 2 emissions, the cost and the water.
	Indicators indicators.Values `json:"indicators"`
	// MeasuredShare is the part of ComponentsJ that was measured rather
	// than estimated.
	MeasuredShare float64 `json:"measured_share"`
	// UnattributedJ is the component energy no backend accounts for.
	UnattributedJ float64    `json:"unattributed_j"`
	Backends      []ESGGroup `json:"backends"`
	Models        []ESGGroup `json:"models"`
	SCI           SCIRow     `json:"sci"`
	SCIMissing    []string   `json:"sci_missing"`
}

// MethodScope2Location names the accounting method of the report.
const MethodScope2Location = "ghg_protocol_scope2_location_based"

// ESGInput is what the environmental report of a period is built from.
type ESGInput struct {
	From, To, Now time.Time
	Version       string
	Factors       api.Factors
	// Channels are the node rows of the period with their indicators.
	Channels []store.ChannelEnergy
	Backends []store.UsageEnergy
	Models   []store.UsageEnergy
	SCI      SCIReport
}

// BuildESG adds up the energy of the node by domain and the use of every
// backend and model.
func BuildESG(in ESGInput) ESGReport {
	rep := ESGReport{
		SchemaVersion: SchemaVersion, From: in.From, To: in.To, GeneratedAt: in.Now, Version: in.Version,
		Method: MethodScope2Location, Factors: in.Factors, Domains: []ESGDomain{},
		Backends: groups(in.Backends), Models: groups(in.Models), SCI: in.SCI.Node, SCIMissing: in.SCI.Missing,
	}
	if rep.SCIMissing == nil {
		rep.SCIMissing = []string{}
	}
	at := map[source.Domain]int{}
	measured := 0.0
	for _, c := range in.Channels {
		comp := c.Domain.Level() == source.LevelComponent
		if comp {
			rep.ComponentsJ += c.EnergyJ
			if c.Quality == string(source.QualityMeasured) {
				measured += c.EnergyJ
			}
			if c.Indicators != nil {
				rep.Indicators.Add(*c.Indicators)
			}
		}
		i, ok := at[c.Domain]
		if !ok {
			at[c.Domain] = len(rep.Domains)
			rep.Domains = append(rep.Domains, ESGDomain{Domain: c.Domain, Component: comp, Quality: c.Quality})
			i = len(rep.Domains) - 1
		}
		d := &rep.Domains[i]
		d.EnergyJ += c.EnergyJ
		// A domain read by an estimate anywhere is reported as estimated.
		if c.Quality == string(source.QualityEstimated) {
			d.Quality = c.Quality
		}
	}
	slices.SortFunc(rep.Domains, func(a, b ESGDomain) int {
		return cmp.Or(-cmp.Compare(boolInt(a.Component), boolInt(b.Component)), cmp.Compare(a.Domain, b.Domain))
	})
	rep.FacilityKWh = rep.ComponentsJ / indicators.JoulesPerKWh * in.Factors.PUE
	rep.MeasuredShare = indicators.Share(measured, rep.ComponentsJ)
	attributed := 0.0
	for _, b := range rep.Backends {
		attributed += b.EnergyJ
	}
	rep.UnattributedJ = max(0, rep.ComponentsJ-attributed)
	return rep
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func groups(rows []store.UsageEnergy) []ESGGroup {
	out := []ESGGroup{}
	for _, r := range rows {
		g := ESGGroup{Backend: r.Backend, Kind: r.Kind, Model: r.Model, EnergyJ: r.EnergyJ,
			GeneratedTokens: r.GeneratedTokens, Requests: r.Requests}
		if r.GeneratedTokens > 0 {
			g.JPerToken = r.EnergyJ / float64(r.GeneratedTokens)
		}
		if r.Indicators != nil {
			g.Indicators = *r.Indicators
		}
		out = append(out, g)
	}
	slices.SortStableFunc(out, func(a, b ESGGroup) int { return cmp.Compare(b.EnergyJ, a.EnergyJ) })
	return out
}
