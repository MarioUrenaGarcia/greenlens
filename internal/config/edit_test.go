// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package config

import (
	"strings"
	"testing"
	"time"
)

func TestSetAlertRules(t *testing.T) {
	orig := []byte("# Comment that stays.\nagent:\n  sample_interval: 2s # also this one\nalerts:\n  sinks:\n    log: false\n")
	rules := []AlertRule{
		{Name: "high_jpt", Expr: "model_joules_per_token", Op: ">", Threshold: 5, For: Duration(10 * time.Minute)},
		{Name: "rapl_down", Expr: "source_up", Op: "<", Threshold: 1, Match: map[string]string{"source": "rapl"}},
	}
	out, err := SetAlertRules(orig, rules)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "# Comment that stays.") || !strings.Contains(text, "# also this one") || !strings.Contains(text, `op: ">"`) ||
		!strings.Contains(text, "threshold: 5\n") || !strings.Contains(text, `for: "10m"`) {
		t.Fatalf("lost comments or quoting:\n%s", text)
	}
	cfg := Defaults()
	if err := Parse(out, &cfg); err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	got := cfg.Alerts.Rules
	if len(got) != 2 || got[0].Name != "high_jpt" || got[0].For.D() != 10*time.Minute || got[0].Threshold != 5 ||
		got[1].Match["source"] != "rapl" || cfg.Alerts.Sinks.Log || cfg.Agent.SampleInterval.D() != 2*time.Second {
		t.Fatalf("parsed %+v\n%s", cfg.Alerts, text)
	}
	// No rules leaves an empty list, and an empty file gets the key.
	out, err = SetAlertRules(nil, nil)
	if err != nil || !strings.Contains(string(out), "rules: []") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestAlertRulesAreValidated(t *testing.T) {
	cfg := Defaults()
	cfg.Alerts.Rules = []AlertRule{{Name: "a", Expr: "cpu_temperature", Op: ">"}, {Name: "b", Expr: "node_power_watts", Op: ">"}, {Name: "b", Expr: "node_power_watts", Op: "<"}}
	cfg.Alerts.Sinks.Webhook = AlertWebhook{Enabled: true, URL: "https://192.0.2.7/hook"}
	msg := cfg.Validate().Error()
	for _, want := range []string{"alerts.rules[0]", "alerts.rules[2].name", "alerts.sinks.webhook.url", "alerts.sinks.webhook.token_file"} {
		if !strings.Contains(msg, want) {
			t.Errorf("no error for %s in %s", want, msg)
		}
	}
}

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "0s", 30 * time.Second: "30s", 5 * time.Minute: "5m", 90 * time.Minute: "1h30m",
		time.Hour: "1h", 90 * time.Second: "1m30s", 1500 * time.Millisecond: "1.5s"} {
		if got := ShortDuration(d); got != want {
			t.Errorf("%s: %s, want %s", d, got, want)
		}
	}
}
