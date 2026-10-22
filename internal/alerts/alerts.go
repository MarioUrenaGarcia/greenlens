// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package alerts evaluates alert rules over the signals of the agent. A
// rule compares one signal with a threshold; while the comparison holds, an
// alert is pending, it fires once it has held for the duration of the rule,
// and it is resolved when it stops holding. The package keeps the state of
// every alert and reports each change, so the agent can store it and send
// notifications.
package alerts

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// Signals an alert rule can compare, each with the labels its series
// carry.
const (
	// NodePowerW is the power of the components of the node in the latest
	// sample: processor package, memory and GPU.
	NodePowerW = "node_power_watts"
	// BackendPowerW is the power of each backend over the latest usage
	// interval.
	BackendPowerW = "backend_power_watts"
	// ModelJPerToken is the energy per generated token of each model over
	// the last Window.
	ModelJPerToken = "model_joules_per_token" //nolint:gosec // the name of a signal, not a credential
	// ModelJPTZScore is the z-score of the energy per generated token of
	// each model today against the days before, as regressions judge it.
	ModelJPTZScore = "model_jpt_zscore"
	// SourceUp is 1 for each energy source that is selected and returned
	// values in the latest sample, and 0 otherwise.
	SourceUp = "source_up"
)

// Signals lists every signal, in the order of the documentation.
var Signals = []string{NodePowerW, BackendPowerW, ModelJPerToken, ModelJPTZScore, SourceUp}

// Window is the span over which ModelJPerToken is computed.
const Window = 15 * time.Minute

// MinWindowTokens is the fewest generated tokens in Window for a model to
// have a ModelJPerToken series; fewer make the ratio noise.
const MinWindowTokens = 100

// Labels each signal carries, which rules may match.
var signalLabels = map[string][]string{
	NodePowerW:     nil,
	BackendPowerW:  {"backend"},
	ModelJPerToken: {"backend", "model"},
	ModelJPTZScore: {"backend", "model"},
	SourceUp:       {"source"},
}

// LabelsOf returns the labels the series of a signal carry.
func LabelsOf(signal string) []string { return signalLabels[signal] }

// Operators compare a value with the threshold of a rule.
var Operators = []string{">", ">=", "<", "<="}

func compare(op string, v, threshold float64) bool {
	switch op {
	case ">":
		return v > threshold
	case ">=":
		return v >= threshold
	case "<":
		return v < threshold
	case "<=":
		return v <= threshold
	}
	return false
}

// Rule is one alert rule. Match, when set, keeps only the series whose
// labels have those values.
type Rule struct {
	Name      string            `json:"name"`
	Expr      string            `json:"expr"`
	Op        string            `json:"op"`
	Threshold float64           `json:"threshold"`
	For       time.Duration     `json:"-"`
	Match     map[string]string `json:"match,omitempty"`
}

// Validate reports the first problem of a rule.
func (r Rule) Validate() error {
	switch {
	case strings.TrimSpace(r.Name) == "":
		return fmt.Errorf("a rule needs a name")
	case !slices.Contains(Signals, r.Expr):
		return fmt.Errorf("rule %s: expr must be one of %s", r.Name, strings.Join(Signals, ", "))
	case !slices.Contains(Operators, r.Op):
		return fmt.Errorf("rule %s: op must be one of %s", r.Name, strings.Join(Operators, " "))
	case r.For < 0:
		return fmt.Errorf("rule %s: for must not be negative", r.Name)
	}
	for k := range r.Match {
		if !slices.Contains(signalLabels[r.Expr], k) {
			return fmt.Errorf("rule %s: %s has no label %q to match", r.Name, r.Expr, k)
		}
	}
	return nil
}

// ValidateRules checks every rule and that names are unique.
func ValidateRules(rules []Rule) error {
	seen := map[string]bool{}
	for _, r := range rules {
		if err := r.Validate(); err != nil {
			return err
		}
		if seen[r.Name] {
			return fmt.Errorf("rule %s: the name is used twice", r.Name)
		}
		seen[r.Name] = true
	}
	return nil
}

// Series is one value of a signal, with its labels.
type Series struct {
	Labels map[string]string
	Value  float64
}

// Values holds the series of each signal at one evaluation. A signal that
// could not be computed is absent, and its alerts keep their state.
type Values map[string][]Series

// States of an alert.
const (
	Pending  = "pending"
	Firing   = "firing"
	Resolved = "resolved"
)

// Alert is one alert: a rule and the labels of one series.
type Alert struct {
	// ID is the row of the alert in the store, set by the caller.
	ID        int64             `json:"id"`
	Rule      string            `json:"rule"`
	Expr      string            `json:"expr"`
	Op        string            `json:"op"`
	Threshold float64           `json:"threshold"`
	ForS      float64           `json:"for_s"`
	Labels    map[string]string `json:"labels"`
	State     string            `json:"state"`
	// Value is the value of the series at the latest change of state.
	Value      float64    `json:"value"`
	StartedAt  time.Time  `json:"started_at"`
	FiredAt    *time.Time `json:"fired_at,omitempty"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

// Key identifies an alert by its rule and labels.
func Key(rule string, labels map[string]string) string {
	var b strings.Builder
	b.WriteString(rule)
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		b.WriteString("\x00" + k + "=" + labels[k])
	}
	return b.String()
}

// Engine keeps the alerts that are pending or firing.
type Engine struct {
	rules  []Rule
	active map[string]*Alert
}

// NewEngine returns an engine for rules, with the alerts that were pending
// or firing when the agent stopped.
func NewEngine(rules []Rule, restored []Alert) *Engine {
	e := &Engine{rules: slices.Clone(rules), active: map[string]*Alert{}}
	for _, a := range restored {
		if a.State == Pending || a.State == Firing {
			c := a
			e.active[Key(a.Rule, a.Labels)] = &c
		}
	}
	return e
}

// Rules returns the rules of the engine.
func (e *Engine) Rules() []Rule { return slices.Clone(e.rules) }

// SetRules replaces the rules. The alerts of a rule that is gone, or whose
// signal changed, are resolved at now and returned.
func (e *Engine) SetRules(rules []Rule, now time.Time) []Alert {
	byName := map[string]Rule{}
	for _, r := range rules {
		byName[r.Name] = r
	}
	var out []Alert
	for k, a := range e.active {
		r, ok := byName[a.Rule]
		if !ok || r.Expr != a.Expr || !matches(r.Match, a.Labels) {
			out = append(out, e.resolve(k, a, a.Value, now))
			continue
		}
		// The next evaluation judges the alert with the new comparison.
		a.Op, a.Threshold, a.ForS = r.Op, r.Threshold, r.For.Seconds()
	}
	e.rules = slices.Clone(rules)
	sortAlerts(out)
	return out
}

func matches(match, labels map[string]string) bool {
	for k, v := range match {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// Evaluate compares every rule with the values at now and returns the
// alerts whose state changed: new pending alerts, alerts that fired and
// alerts that were resolved. A pending alert whose condition stops holding
// before it fires is returned with the state Resolved and no FiredAt.
func (e *Engine) Evaluate(now time.Time, vals Values) []Alert {
	var out []Alert
	for _, r := range e.rules {
		series, known := vals[r.Expr]
		if !known {
			continue
		}
		holding := map[string]bool{}
		for _, s := range series {
			if !matches(r.Match, s.Labels) {
				continue
			}
			k := Key(r.Name, s.Labels)
			if !compare(r.Op, s.Value, r.Threshold) {
				continue
			}
			holding[k] = true
			a, ok := e.active[k]
			if !ok {
				a = &Alert{Rule: r.Name, Expr: r.Expr, Op: r.Op, Threshold: r.Threshold, ForS: r.For.Seconds(),
					Labels: maps.Clone(s.Labels), State: Pending, Value: s.Value, StartedAt: now}
				if a.Labels == nil {
					a.Labels = map[string]string{}
				}
				e.active[k] = a
				if r.For > 0 {
					out = append(out, *a)
					continue
				}
			}
			if a.State == Pending && now.Sub(a.StartedAt) >= r.For {
				t := now
				a.State, a.FiredAt, a.Value = Firing, &t, s.Value
				out = append(out, *a)
			}
		}
		// A series that is gone, or no longer holds, ends its alert.
		for k, a := range e.active {
			if a.Rule != r.Name || holding[k] {
				continue
			}
			v := a.Value
			for _, s := range series {
				if Key(r.Name, s.Labels) == k {
					v = s.Value
				}
			}
			out = append(out, e.resolve(k, a, v, now))
		}
	}
	sortAlerts(out)
	return out
}

func (e *Engine) resolve(k string, a *Alert, v float64, now time.Time) Alert {
	t := now
	a.State, a.ResolvedAt, a.Value = Resolved, &t, v
	delete(e.active, k)
	return *a
}

// SetID records the store row of an alert that is still active.
func (e *Engine) SetID(a Alert, id int64) {
	if x, ok := e.active[Key(a.Rule, a.Labels)]; ok {
		x.ID = id
	}
}

// Active returns the pending and firing alerts, firing first.
func (e *Engine) Active() []Alert {
	out := make([]Alert, 0, len(e.active))
	for _, a := range e.active {
		out = append(out, *a)
	}
	slices.SortFunc(out, func(x, y Alert) int {
		return cmp.Or(cmp.Compare(rank(x.State), rank(y.State)), x.StartedAt.Compare(y.StartedAt), cmp.Compare(Key(x.Rule, x.Labels), Key(y.Rule, y.Labels)))
	})
	return out
}

func rank(state string) int {
	if state == Firing {
		return 0
	}
	return 1
}

func sortAlerts(as []Alert) {
	slices.SortFunc(as, func(x, y Alert) int { return cmp.Compare(Key(x.Rule, x.Labels), Key(y.Rule, y.Labels)) })
}
