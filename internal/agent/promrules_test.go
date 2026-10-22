// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"os"
	"regexp"
	"testing"

	"go.yaml.in/yaml/v3"
)

// The rules for Prometheus use only metrics the agent exports.
func TestPrometheusRulesUseExportedMetrics(t *testing.T) {
	data, err := os.ReadFile("../../deploy/prometheus/rules.yml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Groups []struct {
			Name  string `yaml:"name"`
			Rules []struct {
				Alert       string            `yaml:"alert"`
				Expr        string            `yaml:"expr"`
				For         string            `yaml:"for"`
				Labels      map[string]string `yaml:"labels"`
				Annotations map[string]string `yaml:"annotations"`
			} `yaml:"rules"`
		} `yaml:"groups"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	// Families without samples are left out of /metrics, so the names
	// come from where the agent declares them.
	src, err := os.ReadFile("metrics.go")
	if err != nil {
		t.Fatal(err)
	}
	exported := map[string]bool{}
	for _, m := range regexp.MustCompile(`Name: "(greenlens_[a-z0-9_]+)"`).FindAllStringSubmatch(string(src), -1) {
		exported[m[1]] = true
	}
	metric := regexp.MustCompile(`greenlens_[a-z0-9_]+`)
	n := 0
	for _, g := range doc.Groups {
		for _, rule := range g.Rules {
			n++
			if rule.Alert == "" || rule.Expr == "" || rule.Annotations["summary"] == "" {
				t.Errorf("incomplete rule %+v", rule)
			}
			for _, m := range metric.FindAllString(rule.Expr, -1) {
				if !exported[m] {
					t.Errorf("%s uses %s, which the agent does not export", rule.Alert, m)
				}
			}
		}
	}
	if n < 4 {
		t.Fatalf("only %d rules", n)
	}
}
