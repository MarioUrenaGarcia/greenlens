// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"cmp"
	"net/http"
	"slices"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/analysis"
	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

func (a *Agent) handleRegressions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, err1 := time.Parse(time.RFC3339, q.Get("from"))
	to, err2 := time.Parse(time.RFC3339, q.Get("to"))
	if err1 != nil || err2 != nil || !from.Before(to) || to.Sub(from) > maxRange {
		writeError(w, http.StatusBadRequest, "from and to must be RFC 3339 times with from before to, at most three years apart")
		return
	}
	a.mu.RLock()
	rc := a.cfg.Analysis.Regression
	loc := a.cfg.Factors().Location
	a.mu.RUnlock()
	if loc == nil {
		loc = time.UTC
	}
	p := analysis.Params{BaselineDays: rc.BaselineDays, MinBaselineDays: rc.MinBaselineDays, Z: rc.ZThreshold, MinTokens: int64(rc.MinGeneratedTokens)}
	// Days start at local midnight; the first judged day needs its whole
	// baseline before it.
	day := func(t time.Time) time.Time {
		l := t.In(loc)
		return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
	}
	first := day(from)
	start := first.AddDate(0, 0, -p.BaselineDays)
	rows, _, err := a.st.Usage(r.Context(), start, to, store.ByModel, time.Hour, time.Now(), a.retention())
	if err != nil {
		a.log.Error("regression query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "regression query failed")
		return
	}
	type key struct{ backend, model string }
	byModel := map[key]map[time.Time]*analysis.Day{}
	for _, row := range rows {
		if row.Model == "" || !row.Counted {
			continue
		}
		if b, m := q.Get("backend"), q.Get("model"); (b != "" && b != row.Backend) || (m != "" && m != row.Model) {
			continue
		}
		k := key{row.Backend, row.Model}
		if byModel[k] == nil {
			byModel[k] = map[time.Time]*analysis.Day{}
		}
		d := day(row.BucketStart)
		if byModel[k][d] == nil {
			byModel[k][d] = &analysis.Day{Start: d}
		}
		byModel[k][d].EnergyJ += row.EnergyJ
		byModel[k][d].GeneratedTokens += row.GeneratedTokens
	}
	out := api.Regressions{SchemaVersion: api.SchemaVersion, From: from, To: to, TimeZone: loc.String(), Params: p, Models: []api.ModelRegressions{}}
	for k, ds := range byModel {
		var days []analysis.Day
		for _, d := range ds {
			days = append(days, *d)
		}
		slices.SortFunc(days, func(x, y analysis.Day) int { return x.Start.Compare(y.Start) })
		out.Models = append(out.Models, api.ModelRegressions{Backend: k.backend, Model: k.model, Days: analysis.Evaluate(days, first, p)})
	}
	slices.SortFunc(out.Models, func(x, y api.ModelRegressions) int {
		return cmp.Or(cmp.Compare(x.Backend, y.Backend), cmp.Compare(x.Model, y.Model))
	})
	if out.Events, err = a.st.ContextEvents(r.Context(), first.AddDate(0, 0, -1), to); err != nil {
		a.log.Error("context query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "context query failed")
		return
	}
	writeJSON(w, http.StatusOK, out)
}
