// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"context"
	"encoding/csv"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/report"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

// Kinds and formats of reports.
const (
	reportSCI  = "sci"
	formatJSON = "json"
	formatCSV  = "csv"
)

// apiFactors describes the factors in force.
func apiFactors(f indicators.Factors) api.Factors {
	return api.Factors{EmissionGPerKWh: f.EmissionGPerKWh, EmissionSource: f.EmissionSource, PUE: f.PUE,
		TariffPerKWh: f.TariffPerKWh, Currency: f.Currency, WUELPerKWh: f.WUELPerKWh,
		HourlyProfile: f.Profile != nil, TariffPeriods: len(f.Periods), TimeZone: f.Location.String()}
}

func (a *Agent) handleReport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, err1 := time.Parse(time.RFC3339, q.Get("from"))
	to, err2 := time.Parse(time.RFC3339, q.Get("to"))
	if err1 != nil || err2 != nil || !from.Before(to) || to.Sub(from) > maxRange {
		writeError(w, http.StatusBadRequest, "from and to must be RFC 3339 times with from before to, at most three years apart")
		return
	}
	kind := q.Get("kind")
	if kind != reportSCI {
		writeError(w, http.StatusBadRequest, "kind must be sci")
		return
	}
	format := q.Get("format")
	if format == "" {
		format = formatJSON
	}
	if format != formatJSON && format != formatCSV {
		writeError(w, http.StatusBadRequest, "format must be json or csv")
		return
	}
	groupBy := q.Get("group_by")
	if groupBy != "" && groupBy != store.ByBackend && groupBy != store.ByModel && groupBy != store.ByConsumer {
		writeError(w, http.StatusBadRequest, "group_by must be backend, model or consumer")
		return
	}
	a.mu.RLock()
	unit := a.cfg.Indicators.SCI.FunctionalUnit
	a.mu.RUnlock()
	if u := q.Get("unit"); u != "" {
		unit = u
	}
	if !slices.Contains(indicators.FunctionalUnits, unit) {
		writeError(w, http.StatusBadRequest, "unit must be 1k_generated_tokens, request or consumer")
		return
	}
	rep, err := a.sciReport(r.Context(), from, to, groupBy, unit)
	if err != nil {
		a.log.Error("report query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "report query failed")
		return
	}
	if format == formatCSV {
		writeSCICSV(w, rep)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// nodeTotals adds up the energy of the component domains of the node and
// its indicators.
func (a *Agent) nodeTotals(ctx context.Context, f indicators.Factors, from, to, now time.Time, ret store.Retention) (report.Node, error) {
	query := a.st.NodeEnergy
	if f.Varies() {
		query = a.st.NodeEnergyHourly
	}
	rows, _, err := query(ctx, from, to, 0, now, ret)
	if err != nil {
		return report.Node{}, err
	}
	var n report.Node
	for _, r := range nodeIndicators(rows, f, from, to, 0, f.Varies()) {
		// Platform and wall meters measure the components again.
		if r.Domain.Level() != source.LevelComponent {
			continue
		}
		if r.Domain == source.DomainGPU {
			n.GPUJ += r.EnergyJ
		} else {
			n.ServerJ += r.EnergyJ
		}
		n.Indicators.Add(*r.Indicators)
	}
	return n, nil
}

func (a *Agent) usageRows(ctx context.Context, f indicators.Factors, from, to, now time.Time, by string, ret store.Retention) ([]store.UsageEnergy, error) {
	query := a.st.Usage
	if f.Varies() {
		query = a.st.UsageHourly
	}
	rows, _, err := query(ctx, from, to, by, 0, now, ret)
	if err != nil {
		return nil, err
	}
	return usageIndicators(rows, f, from, to, 0, f.Varies()), nil
}

func (a *Agent) sciReport(ctx context.Context, from, to time.Time, groupBy, unit string) (report.SCIReport, error) {
	a.mu.RLock()
	f, e := a.cfg.Factors(), a.cfg.EmbodiedCarbon()
	a.mu.RUnlock()
	now, ret := time.Now(), a.retention()
	node, err := a.nodeTotals(ctx, f, from, to, now, ret)
	if err != nil {
		return report.SCIReport{}, err
	}
	backends, _, err := a.st.Usage(ctx, from, to, store.ByBackend, 0, now, ret)
	if err != nil {
		return report.SCIReport{}, err
	}
	for _, b := range backends {
		node.GeneratedTokens += b.GeneratedTokens
		node.Requests += b.Requests
	}
	consumers := map[string]int64{}
	if unit == indicators.UnitConsumer {
		rows, _, err := a.st.Usage(ctx, from, to, store.ByConsumer, 0, now, ret)
		if err != nil {
			return report.SCIReport{}, err
		}
		seen := map[string]bool{}
		for _, r := range rows {
			if r.Consumer == "" {
				continue
			}
			consumers[r.Backend]++
			seen[r.Consumer] = true
		}
		node.Consumers = int64(len(seen))
	}
	var groups []store.UsageEnergy
	if groupBy != "" {
		if groups, err = a.usageRows(ctx, f, from, to, now, groupBy, ret); err != nil {
			return report.SCIReport{}, err
		}
	}
	return report.BuildSCI(report.SCIInput{
		From: from, To: to, GroupBy: groupBy, Unit: unit, Factors: apiFactors(f), Embodied: e,
		Node: node, Groups: groups, Consumers: consumers,
	}), nil
}

// writeSCICSV writes the node and every group, with the column names of
// the JSON document.
func writeSCICSV(w http.ResponseWriter, rep report.SCIReport) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
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
	line := func(scope string, r report.SCIRow) {
		_ = cw.Write([]string{scope, r.Backend, r.Model, r.Consumer, num(r.EnergyJ), num(r.ServerShare), num(r.GPUShare),
			num(r.SCI.OperationalG), opt(r.SCI.EmbodiedG), r.SCI.Unit, num(r.SCI.Units), opt(r.SCI.GPerUnit)})
	}
	line("node", rep.Node)
	for _, r := range rep.Rows {
		line(rep.GroupBy, r)
	}
	cw.Flush()
}
