// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/alerts"
	"github.com/MarioUrenaGarcia/greenlens/internal/analysis"
	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

const (
	// alertEvery is how often the alert rules are judged.
	alertEvery = 15 * time.Second
	// zscoreEvery is how often the regressions of the models are computed
	// again for model_jpt_zscore; the days they judge change slowly.
	zscoreEvery = 10 * time.Minute
	// historyLimit bounds the alerts of a period in GET PathAlerts.
	historyLimit = 500
	// historyDefault is the period of the history when none is asked.
	historyDefault = 7 * 24 * time.Hour
	eventAlert     = "alert"
)

// alerting holds the engine of the alerts and what the signals cache.
type alerting struct {
	mu        sync.Mutex
	engine    *alerts.Engine
	evaluated time.Time
	sinks     []string
	zAt       time.Time
	z         []alerts.Series
	zOK       bool
}

// setupAlerts starts the engine with the alerts that were active when the
// agent stopped.
func (a *Agent) setupAlerts(ctx context.Context) {
	restored, err := a.st.ActiveAlerts(ctx)
	if err != nil {
		a.log.Warn("cannot load the active alerts", "error", err)
	}
	sinks := a.alertSinks(a.cfg)
	a.alerting = &alerting{engine: alerts.NewEngine(a.cfg.AlertRules(), restored), sinks: sinkNames(sinks)}
	a.dispatch = alerts.NewDispatcher(a.log, sinks)
}

func sinkNames(sinks []alerts.Sink) []string {
	out := make([]string, 0, len(sinks))
	for _, s := range sinks {
		out = append(out, s.Name())
	}
	return out
}

// readToken reads a token file; surrounding white space is not part of it.
func readToken(path string) ([]byte, error) {
	b, err := os.ReadFile(path) //nolint:gosec // a path of the configuration
	if err != nil {
		return nil, err
	}
	t := []byte(strings.TrimSpace(string(b)))
	if len(t) == 0 {
		return nil, errors.New("the file is empty")
	}
	return t, nil
}

func (a *Agent) alertSinks(cfg config.Config) []alerts.Sink {
	s := cfg.Alerts.Sinks
	var out []alerts.Sink
	if s.Log {
		out = append(out, alerts.LogSink{Log: a.log})
	}
	if s.Journald {
		out = append(out, alerts.JournalSink{Socket: alerts.JournalSocket})
	}
	if s.Syslog {
		out = append(out, alerts.SyslogSink{Socket: alerts.SyslogSocket})
	}
	if w := s.Webhook; w.Enabled {
		token, err := readToken(w.TokenFile)
		if err != nil {
			a.log.Error("alert webhook off: its token cannot be read", "path", w.TokenFile, "error", err)
			return out
		}
		hook, err := alerts.NewWebhook(w.URL, token)
		if err != nil {
			a.log.Error("alert webhook off", "error", err)
			return out
		}
		out = append(out, hook)
	}
	return out
}

// alertPaths returns the paths the sinks need once the agent confines its
// file access: the directory of the token file and the sockets of the log
// daemons.
func alertPaths(cfg config.Config) (readOnly, sockets []string) {
	s := cfg.Alerts.Sinks
	if s.Webhook.Enabled && s.Webhook.TokenFile != "" {
		readOnly = append(readOnly, filepath.Dir(s.Webhook.TokenFile))
	}
	if s.Journald {
		sockets = append(sockets, alerts.JournalSocket)
	}
	if s.Syslog {
		sockets = append(sockets, alerts.SyslogSocket)
		// /dev/log is usually a link to the socket of journald.
		if p, err := filepath.EvalSymlinks(alerts.SyslogSocket); err == nil && p != alerts.SyslogSocket {
			sockets = append(sockets, p)
		}
	}
	return readOnly, sockets
}

// reloadAlerts applies new rules and sinks. Sinks that need a path the
// confinement did not allow keep their settings until a restart.
func (a *Agent) reloadAlerts(ctx context.Context, old config.Config, cfg *config.Config) {
	o, n := old.Alerts.Sinks, &cfg.Alerts.Sinks
	if o.Journald != n.Journald || o.Syslog != n.Syslog || o.Webhook.TokenFile != n.Webhook.TokenFile {
		a.log.Warn("alerts.sinks.journald, alerts.sinks.syslog and alerts.sinks.webhook.token_file change only after a restart")
		n.Journald, n.Syslog, n.Webhook.TokenFile = o.Journald, o.Syslog, o.Webhook.TokenFile
	}
	sinks := a.alertSinks(*cfg)
	a.dispatch.SetSinks(sinks)
	a.alerting.mu.Lock()
	a.alerting.sinks = sinkNames(sinks)
	changes := a.alerting.engine.SetRules(cfg.AlertRules(), time.Now().UTC())
	a.alerting.mu.Unlock()
	a.applyAlerts(ctx, changes)
}

// evaluateAlerts judges every rule with the signals at now.
func (a *Agent) evaluateAlerts(ctx context.Context, now time.Time) {
	// Alerts keep UTC times, as the store gives them back.
	now = now.UTC()
	a.alerting.mu.Lock()
	idle := len(a.alerting.engine.Rules()) == 0 && len(a.alerting.engine.Active()) == 0
	if idle {
		a.alerting.evaluated = now
	}
	a.alerting.mu.Unlock()
	// Without rules there is nothing to judge, and the signals are not
	// read.
	if idle {
		return
	}
	vals := a.alertValues(ctx, now)
	a.alerting.mu.Lock()
	changes := a.alerting.engine.Evaluate(now, vals)
	a.alerting.evaluated = now
	a.alerting.mu.Unlock()
	a.applyAlerts(ctx, changes)
}

// applyAlerts stores the alerts that changed and notifies those that fired
// or were resolved. A pending alert that ends before firing leaves no
// history.
func (a *Agent) applyAlerts(ctx context.Context, changes []alerts.Alert) {
	for _, al := range changes {
		var err error
		switch {
		case al.State == alerts.Resolved && al.FiredAt == nil:
			if al.ID != 0 {
				err = a.st.DeleteAlert(ctx, al.ID)
			}
		case al.ID == 0:
			var id int64
			if id, err = a.st.InsertAlert(ctx, al); err == nil {
				al.ID = id
				a.alerting.mu.Lock()
				a.alerting.engine.SetID(al, id)
				a.alerting.mu.Unlock()
			}
		default:
			err = a.st.UpdateAlert(ctx, al)
		}
		if err != nil {
			a.limited("alert-store", "storing an alert failed", "rule", al.Rule, "error", err)
		}
		if al.State == alerts.Firing || (al.State == alerts.Resolved && al.FiredAt != nil) {
			a.dispatch.Notify(alerts.Notification{SchemaVersion: api.SchemaVersion, Agent: a.o.Version, Alert: al})
		}
		if a.stream.listening() {
			if data, err := json.Marshal(al); err == nil {
				a.stream.publish(event{name: eventAlert, data: data})
			}
		}
	}
}

// sourcesUp returns, for every energy source the agent knows, whether it is
// selected and returned values in the latest sample. a.mu must be held.
func (a *Agent) sourcesUp() map[string]bool {
	selected := a.sel.Sources()
	owner := map[string]string{}
	for _, ch := range a.sel.Channels {
		owner[ch.ID] = ch.Source
	}
	reading := map[string]bool{}
	for _, c := range a.last.Channels {
		reading[owner[c.Channel]] = true
	}
	out := map[string]bool{}
	for _, c := range a.cands {
		id := c.Source.ID()
		out[id] = slices.Contains(selected, id) && reading[id]
	}
	return out
}

// alertValues computes every signal at now. A signal that cannot be
// computed is left out, so its alerts keep their state.
func (a *Agent) alertValues(ctx context.Context, now time.Time) alerts.Values {
	vals := alerts.Values{}
	a.mu.RLock()
	node, sampled := 0.0, a.samples > 0
	for _, ch := range a.sel.Channels {
		if ch.Domain.Level() == source.LevelComponent {
			node += a.chanW[ch.ID]
		}
	}
	up := a.sourcesUp()
	a.mu.RUnlock()
	if sampled {
		vals[alerts.NodePowerW] = []alerts.Series{{Labels: map[string]string{}, Value: node}}
		ups := []alerts.Series{}
		for id, ok := range up {
			v := 0.0
			if ok {
				v = 1
			}
			ups = append(ups, alerts.Series{Labels: map[string]string{"source": id}, Value: v})
		}
		vals[alerts.SourceUp] = ups
	}
	backends := []alerts.Series{}
	a.bmu.Lock()
	for name, st := range a.bstate {
		if st.present {
			backends = append(backends, alerts.Series{Labels: map[string]string{"backend": name}, Value: st.powerW})
		}
	}
	a.bmu.Unlock()
	vals[alerts.BackendPowerW] = backends

	rows, _, err := a.st.Usage(ctx, now.Add(-alerts.Window), now, store.ByModel, 0, now, a.retention())
	if err != nil {
		a.limited("alert-usage", "the energy per token of the alerts could not be read", "error", err)
	} else {
		type sum struct{ j, tokens float64 }
		by := map[modelKey]*sum{}
		for _, r := range rows {
			if r.Model == "" || !r.Counted {
				continue
			}
			k := modelKey{r.Backend, r.Model}
			if by[k] == nil {
				by[k] = &sum{}
			}
			by[k].j += r.EnergyJ
			by[k].tokens += float64(r.GeneratedTokens)
		}
		series := []alerts.Series{}
		for k, s := range by {
			if s.tokens >= alerts.MinWindowTokens {
				series = append(series, alerts.Series{Labels: map[string]string{"backend": k.backend, "model": k.model}, Value: s.j / s.tokens})
			}
		}
		vals[alerts.ModelJPerToken] = series
	}
	if z, ok := a.zscores(ctx, now); ok {
		vals[alerts.ModelJPTZScore] = z
	}
	return vals
}

// zscores returns the z-score of today of every model, as the regressions
// judge it, computed again at most every zscoreEvery.
func (a *Agent) zscores(ctx context.Context, now time.Time) ([]alerts.Series, bool) {
	a.alerting.mu.Lock()
	if a.alerting.zOK && now.Sub(a.alerting.zAt) < zscoreEvery {
		z := a.alerting.z
		a.alerting.mu.Unlock()
		return z, true
	}
	a.alerting.mu.Unlock()
	p, loc := a.regressionParams()
	day := localDay(loc)
	today := day(now)
	byModel, err := a.modelDays(ctx, today.AddDate(0, 0, -p.BaselineDays), now, "", "", day)
	if err != nil {
		a.limited("alert-zscore", "the regressions of the alerts could not be computed", "error", err)
		return nil, false
	}
	out := []alerts.Series{}
	for k, days := range byModel {
		for _, f := range analysis.Evaluate(days, today, p) {
			if f.Day == today.Format(time.DateOnly) && f.Z != nil {
				out = append(out, alerts.Series{Labels: map[string]string{"backend": k.backend, "model": k.model}, Value: *f.Z})
			}
		}
	}
	a.alerting.mu.Lock()
	a.alerting.z, a.alerting.zAt, a.alerting.zOK = out, now, true
	a.alerting.mu.Unlock()
	return out, true
}

func apiRules(rules []config.AlertRule) []api.AlertRule {
	out := make([]api.AlertRule, 0, len(rules))
	for _, r := range rules {
		out = append(out, api.AlertRule{Name: r.Name, Expr: r.Expr, Op: r.Op, Threshold: r.Threshold, For: config.ShortDuration(r.For.D()), Match: r.Match})
	}
	return out
}

func (a *Agent) handleAlerts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	to, from := time.Now(), time.Now().Add(-historyDefault)
	if s := q.Get("to"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "to must be an RFC 3339 time")
			return
		}
		to = t
	}
	if s := q.Get("from"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "from must be an RFC 3339 time")
			return
		}
		from = t
	}
	if !from.Before(to) || to.Sub(from) > maxRange {
		writeError(w, http.StatusBadRequest, "from must be before to, at most three years apart")
		return
	}
	history, err := a.st.AlertHistory(r.Context(), from, to, historyLimit)
	if err != nil {
		a.log.Error("alert history query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "alert history query failed")
		return
	}
	a.mu.RLock()
	rules := apiRules(a.cfg.Alerts.Rules)
	a.mu.RUnlock()
	a.alerting.mu.Lock()
	out := api.Alerts{SchemaVersion: api.SchemaVersion, EveryS: alertEvery.Seconds(), Operators: alerts.Operators, Rules: rules,
		Sinks: slices.Clone(a.alerting.sinks), Writable: a.configWritable, Active: a.alerting.engine.Active(),
		From: from.UTC(), To: to.UTC(), History: history}
	if !a.alerting.evaluated.IsZero() {
		t := a.alerting.evaluated.UTC()
		out.EvaluatedAt = &t
	}
	a.alerting.mu.Unlock()
	for _, s := range alerts.Signals {
		out.Signals = append(out.Signals, api.AlertSignal{Name: s, Labels: append([]string{}, alerts.LabelsOf(s)...)})
	}
	writeJSON(w, http.StatusOK, out)
}

// previewRules puts new rules in the configuration file and checks it.
func (a *Agent) previewRules(r *http.Request) (api.ConfigPreview, []string, error) {
	var body api.AlertRules
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		return api.ConfigPreview{}, nil, err
	}
	out := api.ConfigPreview{SchemaVersion: api.SchemaVersion, Errors: []api.ConfigError{}}
	rules := make([]config.AlertRule, 0, len(body.Rules))
	names := make([]string, 0, len(body.Rules))
	for i, br := range body.Rules {
		var d time.Duration
		if br.For != "" {
			var err error
			if d, err = time.ParseDuration(br.For); err != nil {
				out.Errors = append(out.Errors, api.ConfigError{Key: fmt.Sprintf("alerts.rules[%d].for", i), Message: "must be a duration such as 10m"})
			}
		}
		rules = append(rules, config.AlertRule{Name: br.Name, Expr: br.Expr, Op: br.Op, Threshold: br.Threshold, For: config.Duration(d), Match: br.Match})
		names = append(names, br.Name)
	}
	var current []byte
	if a.o.ConfigPath != "" {
		data, err := os.ReadFile(a.o.ConfigPath)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return out, nil, err
		}
		current = data
	}
	edited, err := config.SetAlertRules(current, rules)
	if err != nil {
		out.Errors = append(out.Errors, api.ConfigError{Message: err.Error()})
		return out, names, nil
	}
	out.YAML = string(edited)
	out.Errors = append(out.Errors, checkConfig(edited)...)
	out.Valid = len(out.Errors) == 0
	return out, names, nil
}

func (a *Agent) handleAlertRulesPreview(w http.ResponseWriter, r *http.Request) {
	out, _, err := a.previewRules(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "the body must be JSON with the rules: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *Agent) handleAlertRulesApply(w http.ResponseWriter, r *http.Request) {
	if !a.configWritable {
		writeError(w, http.StatusConflict, "the agent cannot write its configuration file; install the previewed file instead")
		return
	}
	out, names, err := a.previewRules(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "the body must be JSON with the rules: "+err.Error())
		return
	}
	if !out.Valid {
		writeJSON(w, http.StatusUnprocessableEntity, out)
		return
	}
	if err := a.writeConfig(out.YAML); err != nil {
		a.log.Error("writing the configuration failed", "error", err)
		writeError(w, http.StatusInternalServerError, "writing the configuration failed")
		return
	}
	if err := a.st.Audit(r.Context(), actor(r), "alerts.rules", strings.Join(names, ", ")); err != nil {
		a.log.Warn("audit log write failed", "error", err)
	}
	select {
	case a.selfReload <- struct{}{}:
	default:
	}
	out.Applied = true
	writeJSON(w, http.StatusOK, out)
}
