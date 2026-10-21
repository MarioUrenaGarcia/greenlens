// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package prometheus

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func TestWrite(t *testing.T) {
	var b bytes.Buffer
	err := Write(&b, []Family{
		{Name: "greenlens_node_power_watts", Help: "Power.\nSecond line \\ end", Type: Gauge, Samples: []Sample{
			{Labels: L("domain", "package", "source", `od"d\`), Value: 12.5},
			{Labels: L("domain", "gpu", "source", "nvml\nx"), Value: math.Inf(1)},
		}},
		{Name: "greenlens_empty", Help: "Nothing.", Type: Counter},
		{Name: "greenlens_build_info", Help: "Build.", Type: Gauge, Samples: []Sample{{Value: 1}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `# HELP greenlens_node_power_watts Power.\nSecond line \\ end
# TYPE greenlens_node_power_watts gauge
greenlens_node_power_watts{domain="package",source="od\"d\\"} 12.5
greenlens_node_power_watts{domain="gpu",source="nvml\nx"} +Inf
# HELP greenlens_build_info Build.
# TYPE greenlens_build_info gauge
greenlens_build_info 1
`
	if b.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", b.String(), want)
	}
}

func TestCollectors(t *testing.T) {
	c := NewCollectors()
	c.Observe("source:rapl", 2*time.Millisecond, nil)
	c.Observe("source:rapl", 200*time.Millisecond, errors.New("busy"))
	c.Observe("backend:ollama", 30*time.Millisecond, nil)
	var b bytes.Buffer
	if err := Write(&b, c.Families()); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		`greenlens_collector_errors_total{collector="backend:ollama"} 0`,
		`greenlens_collector_errors_total{collector="source:rapl"} 1`,
		`greenlens_collector_duration_seconds_bucket{collector="source:rapl",le="0.005"} 1`,
		`greenlens_collector_duration_seconds_bucket{collector="source:rapl",le="0.5"} 2`,
		`greenlens_collector_duration_seconds_bucket{collector="source:rapl",le="+Inf"} 2`,
		`greenlens_collector_duration_seconds_count{collector="source:rapl"} 2`,
		`greenlens_collector_duration_seconds_sum{collector="source:rapl"} 0.202`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
	if strings.Index(out, `collector="backend:ollama"`) > strings.Index(out, `collector="source:rapl"`) {
		t.Error("collectors are not sorted")
	}
}
