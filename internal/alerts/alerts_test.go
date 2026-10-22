// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package alerts

import (
	"testing"
	"time"
)

func jpt(v float64) Values {
	return Values{ModelJPerToken: {{Labels: map[string]string{"backend": "ollama", "model": "qwen"}, Value: v}}}
}

func TestLifecycleOfAnAlert(t *testing.T) {
	t0 := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	e := NewEngine([]Rule{{Name: "high_jpt", Expr: ModelJPerToken, Op: ">", Threshold: 5, For: 10 * time.Minute}}, nil)
	// Below the threshold nothing happens.
	if got := e.Evaluate(t0, jpt(4)); len(got) != 0 {
		t.Fatalf("below: %+v", got)
	}
	// Above it the alert is pending until it has held for 10 minutes.
	got := e.Evaluate(t0.Add(15*time.Second), jpt(6))
	if len(got) != 1 || got[0].State != Pending || got[0].Labels["model"] != "qwen" || got[0].FiredAt != nil {
		t.Fatalf("pending: %+v", got)
	}
	e.SetID(got[0], 7)
	if got := e.Evaluate(t0.Add(5*time.Minute), jpt(6)); len(got) != 0 {
		t.Fatalf("still pending: %+v", got)
	}
	got = e.Evaluate(t0.Add(10*time.Minute+15*time.Second), jpt(6.5))
	if len(got) != 1 || got[0].State != Firing || got[0].ID != 7 || got[0].Value != 6.5 || !got[0].FiredAt.Equal(t0.Add(10*time.Minute+15*time.Second)) {
		t.Fatalf("firing: %+v", got)
	}
	if a := e.Active(); len(a) != 1 || a[0].State != Firing {
		t.Fatalf("active: %+v", a)
	}
	// Back below, it is resolved with the value that ended it.
	got = e.Evaluate(t0.Add(20*time.Minute), jpt(4.5))
	if len(got) != 1 || got[0].State != Resolved || got[0].Value != 4.5 || got[0].FiredAt == nil || got[0].ResolvedAt == nil {
		t.Fatalf("resolved: %+v", got)
	}
	if len(e.Active()) != 0 {
		t.Fatal("still active")
	}
}

func TestPendingThatEndsBeforeFiring(t *testing.T) {
	t0 := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	e := NewEngine([]Rule{{Name: "r", Expr: ModelJPerToken, Op: ">=", Threshold: 5, For: time.Minute}}, nil)
	e.Evaluate(t0, jpt(5))
	got := e.Evaluate(t0.Add(30*time.Second), jpt(1))
	if len(got) != 1 || got[0].State != Resolved || got[0].FiredAt != nil {
		t.Fatalf("%+v", got)
	}
}

func TestWithoutForAnAlertFiresAtOnce(t *testing.T) {
	t0 := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	e := NewEngine([]Rule{{Name: "down", Expr: SourceUp, Op: "<", Threshold: 1}}, nil)
	v := Values{SourceUp: {{Labels: map[string]string{"source": "rapl"}, Value: 0}, {Labels: map[string]string{"source": "nvml"}, Value: 1}}}
	got := e.Evaluate(t0, v)
	if len(got) != 1 || got[0].State != Firing || got[0].Labels["source"] != "rapl" || got[0].ID != 0 {
		t.Fatalf("%+v", got)
	}
	// A series that disappears ends its alert.
	got = e.Evaluate(t0.Add(15*time.Second), Values{SourceUp: {{Labels: map[string]string{"source": "nvml"}, Value: 1}}})
	if len(got) != 1 || got[0].State != Resolved {
		t.Fatalf("gone: %+v", got)
	}
}

func TestUnknownSignalKeepsTheState(t *testing.T) {
	t0 := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	e := NewEngine([]Rule{{Name: "r", Expr: ModelJPTZScore, Op: ">", Threshold: 3}}, nil)
	v := Values{ModelJPTZScore: {{Labels: map[string]string{"backend": "b", "model": "m"}, Value: 4}}}
	e.Evaluate(t0, v)
	// A signal that could not be computed this time leaves alerts alone.
	if got := e.Evaluate(t0.Add(time.Minute), Values{}); len(got) != 0 || len(e.Active()) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestMatchAndRestore(t *testing.T) {
	t0 := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	rules := []Rule{{Name: "vllm_power", Expr: BackendPowerW, Op: ">", Threshold: 200, For: 5 * time.Minute, Match: map[string]string{"backend": "vllm-8000"}}}
	started := t0.Add(-10 * time.Minute)
	// A pending alert from before a restart fires when its time is up.
	e := NewEngine(rules, []Alert{{ID: 3, Rule: "vllm_power", Expr: BackendPowerW, Labels: map[string]string{"backend": "vllm-8000"}, State: Pending, StartedAt: started}})
	v := Values{BackendPowerW: {{Labels: map[string]string{"backend": "vllm-8000"}, Value: 250}, {Labels: map[string]string{"backend": "ollama"}, Value: 900}}}
	got := e.Evaluate(t0, v)
	if len(got) != 1 || got[0].State != Firing || got[0].ID != 3 || got[0].Labels["backend"] != "vllm-8000" {
		t.Fatalf("%+v", got)
	}
	// Changing the threshold keeps the alert; removing the rule resolves it.
	rules[0].Threshold = 220
	if out := e.SetRules(rules, t0.Add(time.Minute)); len(out) != 0 || e.Active()[0].Threshold != 220 {
		t.Fatalf("changed rule: %+v", out)
	}
	if out := e.SetRules(nil, t0.Add(2*time.Minute)); len(out) != 1 || out[0].State != Resolved {
		t.Fatalf("removed rule: %+v", out)
	}
}

func TestValidateRules(t *testing.T) {
	ok := Rule{Name: "a", Expr: NodePowerW, Op: ">", Threshold: 1}
	for _, tt := range []struct {
		name  string
		rules []Rule
	}{
		{"no name", []Rule{{Expr: NodePowerW, Op: ">"}}},
		{"unknown signal", []Rule{{Name: "a", Expr: "cpu_temp", Op: ">"}}},
		{"unknown operator", []Rule{{Name: "a", Expr: NodePowerW, Op: "!="}}},
		{"negative for", []Rule{{Name: "a", Expr: NodePowerW, Op: ">", For: -time.Second}}},
		{"label of another signal", []Rule{{Name: "a", Expr: NodePowerW, Op: ">", Match: map[string]string{"model": "x"}}}},
		{"twice", []Rule{ok, ok}},
	} {
		if ValidateRules(tt.rules) == nil {
			t.Errorf("%s: accepted", tt.name)
		}
	}
	if err := ValidateRules([]Rule{ok, {Name: "b", Expr: ModelJPerToken, Op: "<=", Match: map[string]string{"model": "x"}}}); err != nil {
		t.Fatal(err)
	}
}
