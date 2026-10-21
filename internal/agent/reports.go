// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"bytes"
	"cmp"
	"context"
	"encoding/csv"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/i18n"
	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/report"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

// Kinds and formats of reports.
const (
	reportSCI  = "sci"
	reportESG  = "esg"
	formatJSON = "json"
	formatCSV  = "csv"
	formatHTML = "html"
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
	if kind != reportSCI && kind != reportESG {
		writeError(w, http.StatusBadRequest, "kind must be sci or esg")
		return
	}
	format := q.Get("format")
	if format == "" {
		format = formatJSON
	}
	if format != formatJSON && format != formatCSV && format != formatHTML {
		writeError(w, http.StatusBadRequest, "format must be json, csv or html")
		return
	}
	lang := q.Get("lang")
	if lang == "" {
		lang = i18n.Default
	}
	l, err := i18n.New(lang)
	if err != nil {
		writeError(w, http.StatusBadRequest, "lang must be en or es")
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
	failed := func(err error) {
		a.log.Error("report query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "report query failed")
	}
	name := "greenlens-" + kind + "-" + from.In(time.Local).Format(time.DateOnly)
	if kind == reportESG {
		rep, err := a.esgReport(r.Context(), from, to, unit)
		if err != nil {
			failed(err)
			return
		}
		switch format {
		case formatCSV:
			attach(w, name+".csv")
			writeESGCSV(w, rep)
		case formatHTML:
			writeHTML(w, name+".html", func(b io.Writer) error { return report.RenderESG(b, rep, l) })
		default:
			writeJSON(w, http.StatusOK, rep)
		}
		return
	}
	rep, err := a.sciReport(r.Context(), from, to, groupBy, unit)
	if err != nil {
		failed(err)
		return
	}
	switch format {
	case formatCSV:
		attach(w, name+".csv")
		writeSCICSV(w, rep)
	case formatHTML:
		writeHTML(w, name+".html", func(b io.Writer) error { return report.RenderSCI(b, rep, time.Now(), a.o.Version, l) })
	default:
		writeJSON(w, http.StatusOK, rep)
	}
}

// attach names the file a browser saves the answer as.
func attach(w http.ResponseWriter, name string) {
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
}

// writeHTML writes a printable report. Its policy allows the style of the
// reports and nothing else, not even scripts.
func writeHTML(w http.ResponseWriter, name string, render func(io.Writer) error) {
	var b bytes.Buffer
	if err := render(&b); err != nil {
		writeError(w, http.StatusInternalServerError, "report rendering failed")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src "+report.StyleHash()+"; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	h.Set("Content-Disposition", `inline; filename="`+name+`"`)
	_, _ = w.Write(b.Bytes())
}

// esgReport gathers the environmental report of a period: the channels of
// the node, the backends and the models, read at once.
func (a *Agent) esgReport(ctx context.Context, from, to time.Time, unit string) (report.ESGReport, error) {
	a.mu.RLock()
	f, e := a.cfg.Factors(), a.cfg.EmbodiedCarbon()
	a.mu.RUnlock()
	now, ret := time.Now(), a.retention()
	var chans []store.ChannelEnergy
	var backends, models []store.UsageEnergy
	var errs [3]error
	var wg sync.WaitGroup
	wg.Go(func() {
		query := a.st.NodeEnergy
		if f.Varies() {
			query = a.st.NodeEnergyHourly
		}
		var rows []store.ChannelEnergy
		rows, _, errs[0] = query(ctx, from, to, 0, now, ret)
		chans = nodeIndicators(rows, f, from, to, 0, f.Varies())
	})
	wg.Go(func() { backends, errs[1] = a.usageRows(ctx, f, from, to, now, store.ByBackend, ret) })
	wg.Go(func() { models, errs[2] = a.usageRows(ctx, f, from, to, now, store.ByModel, ret) })
	wg.Wait()
	if err := errors.Join(errs[:]...); err != nil {
		return report.ESGReport{}, err
	}
	node := nodeOf(chans)
	for _, b := range backends {
		node.GeneratedTokens += b.GeneratedTokens
		node.Requests += b.Requests
	}
	if unit == indicators.UnitConsumer {
		rows, _, err := a.st.Usage(ctx, from, to, store.ByConsumer, 0, now, ret)
		if err != nil {
			return report.ESGReport{}, err
		}
		node.Consumers = countConsumers(rows, nil)
	}
	fs := apiFactors(f)
	sci := report.BuildSCI(report.SCIInput{From: from, To: to, Unit: unit, Factors: fs, Embodied: e, Node: node})
	return report.BuildESG(report.ESGInput{From: from, To: to, Now: now, Version: a.o.Version, Factors: fs,
		Channels: chans, Backends: backends, Models: models, SCI: sci}), nil
}

// countConsumers counts the distinct consumers of usage rows grouped by
// consumer, and, into per when it is not nil, those of each backend.
func countConsumers(rows []store.UsageEnergy, per map[string]int64) int64 {
	seen := map[string]bool{}
	for _, r := range rows {
		if r.Consumer == "" {
			continue
		}
		if per != nil {
			per[r.Backend]++
		}
		seen[r.Consumer] = true
	}
	return int64(len(seen))
}

// nodeOf adds up the component domains of node rows with indicators.
func nodeOf(rows []store.ChannelEnergy) report.Node {
	var n report.Node
	for _, r := range rows {
		// Platform and wall meters measure the components again.
		if r.Domain.Level() != source.LevelComponent {
			continue
		}
		if r.Domain == source.DomainGPU {
			n.GPUJ += r.EnergyJ
		} else {
			n.ServerJ += r.EnergyJ
		}
		if r.Indicators != nil {
			n.Indicators.Add(*r.Indicators)
		}
	}
	return n
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
	return nodeOf(nodeIndicators(rows, f, from, to, 0, f.Varies())), nil
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
	// The energy of the node and the use of the groups are independent
	// queries; the database answers them at once.
	var node report.Node
	var nodeErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		node, nodeErr = a.nodeTotals(ctx, f, from, to, now, ret)
	}()
	// Every grouping splits all the usage, so the rows of the groups also
	// give the counts of the node.
	var groups, totals []store.UsageEnergy
	var err error
	if groupBy != "" {
		groups, err = a.usageRows(ctx, f, from, to, now, groupBy, ret)
		totals = groups
	} else {
		totals, _, err = a.st.Usage(ctx, from, to, store.ByBackend, 0, now, ret)
	}
	<-done
	if err = cmp.Or(nodeErr, err); err != nil {
		return report.SCIReport{}, err
	}
	for _, g := range totals {
		node.GeneratedTokens += g.GeneratedTokens
		node.Requests += g.Requests
	}
	consumers := map[string]int64{}
	if unit == indicators.UnitConsumer {
		rows := groups
		if groupBy != store.ByConsumer {
			if rows, _, err = a.st.Usage(ctx, from, to, store.ByConsumer, 0, now, ret); err != nil {
				return report.SCIReport{}, err
			}
		}
		node.Consumers = countConsumers(rows, consumers)
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

// writeESGCSV writes the totals, the domains, the backends and the models,
// one per line, with the column names of the JSON document.
func writeESGCSV(w http.ResponseWriter, rep report.ESGReport) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
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
		rows []report.ESGGroup
	}{{"backend", rep.Backends}, {"model", rep.Models}} {
		for _, g := range sec.rows {
			_ = cw.Write([]string{sec.name, "", g.Backend, g.Model, num(g.EnergyJ), num(g.Indicators.KWh), num(g.Indicators.CO2eG),
				opt(g.Indicators.Cost), opt(g.Indicators.WaterL), strconv.FormatInt(g.GeneratedTokens, 10)})
		}
	}
	cw.Flush()
}
