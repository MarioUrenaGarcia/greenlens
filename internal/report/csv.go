// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package report

import (
	"encoding/csv"
	"io"
	"strconv"

	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
)

// WriteSCICSV writes the node and every group, with the column names of
// the JSON document.
func WriteSCICSV(w io.Writer, rep SCIReport) error {
	cw := csv.NewWriter(w)
	num := func(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
	opt := func(v *float64) string {
		if v == nil {
			return ""
		}
		return num(*v)
	}
	_ = cw.Write([]string{"scope", "backend", "model", "consumer", "energy_j", "server_share", "gpu_share",
		"operational_g", "embodied_g", "functional_unit", "units", "g_per_unit"})
	line := func(scope string, r SCIRow) {
		_ = cw.Write([]string{scope, r.Backend, r.Model, r.Consumer, num(r.EnergyJ), num(r.ServerShare), num(r.GPUShare),
			num(r.SCI.OperationalG), opt(r.SCI.EmbodiedG), r.SCI.Unit, num(r.SCI.Units), opt(r.SCI.GPerUnit)})
	}
	line("node", rep.Node)
	for _, r := range rep.Rows {
		line(rep.GroupBy, r)
	}
	cw.Flush()
	return cw.Error()
}

// WriteESGCSV writes the totals, the domains, the backends and the models,
// one per line, with the column names of the JSON document.
func WriteESGCSV(w io.Writer, rep ESGReport) error {
	cw := csv.NewWriter(w)
	num := func(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
	opt := func(v *float64) string {
		if v == nil {
			return ""
		}
		return num(*v)
	}
	_ = cw.Write([]string{"section", "domain", "backend", "model", "energy_j", "kwh", "co2e_g", "cost", "water_l", "generated_tokens"})
	_ = cw.Write([]string{"total", "", "", "", num(rep.ComponentsJ), num(rep.Indicators.KWh), num(rep.Indicators.CO2eG),
		opt(rep.Indicators.Cost), opt(rep.Indicators.WaterL), ""})
	for _, d := range rep.Domains {
		_ = cw.Write([]string{"domain", string(d.Domain), "", "", num(d.EnergyJ), num(d.EnergyJ / indicators.JoulesPerKWh), "", "", "", ""})
	}
	for _, sec := range []struct {
		name string
		rows []ESGGroup
	}{{"backend", rep.Backends}, {"model", rep.Models}} {
		for _, g := range sec.rows {
			_ = cw.Write([]string{sec.name, "", g.Backend, g.Model, num(g.EnergyJ), num(g.Indicators.KWh), num(g.Indicators.CO2eG),
				opt(g.Indicators.Cost), opt(g.Indicators.WaterL), strconv.FormatInt(g.GeneratedTokens, 10)})
		}
	}
	cw.Flush()
	return cw.Error()
}
