// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The dashboard for Grafana uses only metrics and labels the agent
// exports, and every panel explains what it shows.
func TestGrafanaDashboardUsesExportedMetrics(t *testing.T) {
	data, err := os.ReadFile("../../deploy/grafana/greenlens.json")
	if err != nil {
		t.Fatal(err)
	}
	var dash struct {
		Title      string `json:"title"`
		UID        string `json:"uid"`
		Templating struct {
			List []struct {
				Name  string `json:"name"`
				Type  string `json:"type"`
				Query any    `json:"query"`
			} `json:"list"`
		} `json:"templating"`
		Panels []struct {
			ID          int    `json:"id"`
			Type        string `json:"type"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Datasource  struct {
				UID string `json:"uid"`
			} `json:"datasource"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(data, &dash); err != nil {
		t.Fatal(err)
	}
	if dash.Title == "" || dash.UID == "" {
		t.Fatalf("dashboard without title or uid")
	}
	src, err := os.ReadFile("metrics.go")
	if err != nil {
		t.Fatal(err)
	}
	collectors, err := os.ReadFile("../exporter/prometheus/prometheus.go")
	if err != nil {
		t.Fatal(err)
	}
	exported := map[string]bool{}
	for _, m := range regexp.MustCompile(`Name: "(greenlens_[a-z0-9_]+)"`).FindAllStringSubmatch(string(src)+string(collectors), -1) {
		exported[m[1]] = true
	}
	// Labels of the series, and those Prometheus adds to every target.
	labels := map[string]bool{"instance": true, "job": true, "le": true}
	for _, m := range regexp.MustCompile(`L\(((?:"[a-z_]+", [^,)]+,? ?)+)\)`).FindAllStringSubmatch(string(src)+string(collectors), -1) {
		for _, name := range regexp.MustCompile(`"([a-z_]+)", `).FindAllStringSubmatch(m[1], -1) {
			labels[name[1]] = true
		}
	}
	variables := map[string]bool{}
	for _, v := range dash.Templating.List {
		variables[v.Name] = true
	}
	for _, want := range []string{"datasource", "instance", "backend", "model"} {
		if !variables[want] {
			t.Errorf("no %s variable", want)
		}
	}
	metric := regexp.MustCompile(`greenlens_[a-z0-9_]+`)
	matcher := regexp.MustCompile(`([a-z_]+)\s*(=~|!~|!=|=)\s*"`)
	grouping := regexp.MustCompile(`\bby \(([a-z_, ]+)\)`)
	used := regexp.MustCompile(`\$\{?([a-z]+)\}?`)
	ids := map[int]bool{}
	queries := 0
	check := func(where, expr string) {
		for _, m := range metric.FindAllString(expr, -1) {
			base := m
			for _, suffix := range []string{"_bucket", "_sum", "_count"} {
				if s := strings.TrimSuffix(m, suffix); exported[s] {
					base = s
				}
			}
			if !exported[base] {
				t.Errorf("%s uses %s, which the agent does not export", where, m)
			}
		}
		for _, m := range matcher.FindAllStringSubmatch(expr, -1) {
			if !labels[m[1]] {
				t.Errorf("%s matches the label %s, which no series has", where, m[1])
			}
		}
		for _, g := range grouping.FindAllStringSubmatch(expr, -1) {
			for l := range strings.SplitSeq(g[1], ",") {
				if !labels[strings.TrimSpace(l)] {
					t.Errorf("%s groups by the label %s, which no series has", where, strings.TrimSpace(l))
				}
			}
		}
		for _, v := range used.FindAllStringSubmatch(expr, -1) {
			if !variables[v[1]] {
				t.Errorf("%s uses the variable %s, which is not defined", where, v[1])
			}
		}
	}
	for _, v := range dash.Templating.List {
		if q, ok := v.Query.(map[string]any); ok {
			text, _ := q["query"].(string)
			check("variable "+v.Name, text)
		}
	}
	for _, p := range dash.Panels {
		if ids[p.ID] || p.ID == 0 {
			t.Errorf("panel %q has the id %d, which is missing or repeated", p.Title, p.ID)
		}
		ids[p.ID] = true
		if p.Title == "" {
			t.Errorf("panel %d has no title", p.ID)
		}
		if p.Type == "row" {
			continue
		}
		if p.Description == "" || len(p.Targets) == 0 || p.Datasource.UID != "${datasource}" {
			t.Errorf("panel %q lacks a description, a query or the data source variable", p.Title)
		}
		for _, target := range p.Targets {
			queries++
			// Grafana fills in these two; they are not dashboard variables.
			expr := strings.NewReplacer("$__rate_interval", "5m", "$__range", "1h").Replace(target.Expr)
			check("panel "+p.Title, expr)
		}
	}
	if queries < 15 {
		t.Fatalf("only %d queries", queries)
	}
}
