// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"cmp"
	"context"
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
	p, loc := a.regressionParams()
	day := localDay(loc)
	first := day(from)
	start := first.AddDate(0, 0, -p.BaselineDays)
	byModel, err := a.modelDays(r.Context(), start, to, q.Get("backend"), q.Get("model"), day)
	if err != nil {
		a.log.Error("regression query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "regression query failed")
		return
	}
	out := api.Regressions{SchemaVersion: api.SchemaVersion, From: from, To: to, TimeZone: loc.String(), Params: p, Models: []api.ModelRegressions{}}
	for k, days := range byModel {
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

type modelKey struct{ backend, model string }

// regressionParams returns the parameters of the regressions and the time
// zone their days follow.
func (a *Agent) regressionParams() (analysis.Params, *time.Location) {
	a.mu.RLock()
	rc := a.cfg.Analysis.Regression
	loc := a.cfg.Factors().Location
	a.mu.RUnlock()
	if loc == nil {
		loc = time.UTC
	}
	return analysis.Params{BaselineDays: rc.BaselineDays, MinBaselineDays: rc.MinBaselineDays, Z: rc.ZThreshold, MinTokens: int64(rc.MinGeneratedTokens)}, loc
}

// localDay returns the function that gives the local midnight of a time:
// days start there.
func localDay(loc *time.Location) func(time.Time) time.Time {
	return func(t time.Time) time.Time {
		l := t.In(loc)
		return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
	}
}

// modelDays adds up the energy and generated tokens of every model with
// counted tokens, day by day, in [start, to), optionally of one backend or
// model.
func (a *Agent) modelDays(ctx context.Context, start, to time.Time, backend, model string, day func(time.Time) time.Time) (map[modelKey][]analysis.Day, error) {
	rows, _, err := a.st.Usage(ctx, start, to, store.ByModel, time.Hour, time.Now(), a.retention())
	if err != nil {
		return nil, err
	}
	byDay := map[modelKey]map[time.Time]*analysis.Day{}
	for _, row := range rows {
		if row.Model == "" || !row.Counted || (backend != "" && backend != row.Backend) || (model != "" && model != row.Model) {
			continue
		}
		k := modelKey{row.Backend, row.Model}
		if byDay[k] == nil {
			byDay[k] = map[time.Time]*analysis.Day{}
		}
		d := day(row.BucketStart)
		if byDay[k][d] == nil {
			byDay[k][d] = &analysis.Day{Start: d}
		}
		byDay[k][d].EnergyJ += row.EnergyJ
		byDay[k][d].GeneratedTokens += row.GeneratedTokens
	}
	out := map[modelKey][]analysis.Day{}
	for k, ds := range byDay {
		days := make([]analysis.Day, 0, len(ds))
		for _, d := range ds {
			days = append(days, *d)
		}
		slices.SortFunc(days, func(x, y analysis.Day) int { return x.Start.Compare(y.Start) })
		out[k] = days
	}
	return out, nil
}
