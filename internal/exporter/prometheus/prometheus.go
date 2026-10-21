// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package prometheus writes metrics in the Prometheus text exposition
// format, version 0.0.4, and keeps the statistics of the collectors. The
// format is small enough to write directly, so no client library is needed.
package prometheus

import (
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ContentType is the media type of the text format.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// Metric types.
const (
	Counter   = "counter"
	Gauge     = "gauge"
	Histogram = "histogram"
)

// Label is one name and value.
type Label struct {
	Name, Value string
}

// Sample is one value of a family. Suffix is appended to the name of the
// family, as _bucket, _sum or _count for histograms.
type Sample struct {
	Suffix string
	Labels []Label
	Value  float64
}

// Family is a metric with its help text and samples.
type Family struct {
	Name    string
	Help    string
	Type    string
	Samples []Sample
}

// Write writes the families in order. Families without samples are left
// out, as the format allows.
func Write(w io.Writer, fams []Family) error {
	var b strings.Builder
	for _, f := range fams {
		if len(f.Samples) == 0 {
			continue
		}
		b.WriteString("# HELP " + f.Name + " " + escapeHelp(f.Help) + "\n")
		b.WriteString("# TYPE " + f.Name + " " + f.Type + "\n")
		for _, s := range f.Samples {
			b.WriteString(f.Name + s.Suffix)
			if len(s.Labels) > 0 {
				b.WriteByte('{')
				for i, l := range s.Labels {
					if i > 0 {
						b.WriteByte(',')
					}
					b.WriteString(l.Name + `="` + escapeValue(l.Value) + `"`)
				}
				b.WriteByte('}')
			}
			b.WriteString(" " + formatValue(s.Value) + "\n")
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func escapeHelp(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(s)
}

func escapeValue(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}

func formatValue(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// L builds labels from name and value pairs.
func L(pairs ...string) []Label {
	out := make([]Label, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, Label{pairs[i], pairs[i+1]})
	}
	return out
}

// Buckets are the upper bounds, in seconds, of the duration histograms.
var Buckets = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5}

// Collectors counts the reads and errors of each collector, such as an
// energy source or the connector of a backend.
type Collectors struct {
	mu    sync.Mutex
	stats map[string]*stat
}

type stat struct {
	errors  uint64
	count   uint64
	sum     float64
	buckets []uint64
}

// NewCollectors returns empty statistics.
func NewCollectors() *Collectors {
	return &Collectors{stats: map[string]*stat{}}
}

// Observe records one read of a collector.
func (c *Collectors) Observe(name string, d time.Duration, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.stats[name]
	if !ok {
		s = &stat{buckets: make([]uint64, len(Buckets))}
		c.stats[name] = s
	}
	if err != nil {
		s.errors++
	}
	sec := d.Seconds()
	s.count++
	s.sum += sec
	for i, b := range Buckets {
		if sec <= b {
			s.buckets[i]++
		}
	}
}

// Families returns the error counter and the duration histogram.
func (c *Collectors) Families() []Family {
	c.mu.Lock()
	defer c.mu.Unlock()
	names := make([]string, 0, len(c.stats))
	for n := range c.stats {
		names = append(names, n)
	}
	slices.Sort(names)
	errs := Family{Name: "greenlens_collector_errors_total", Help: "Failed reads of each collector.", Type: Counter}
	dur := Family{Name: "greenlens_collector_duration_seconds", Help: "Time taken by the reads of each collector.", Type: Histogram}
	for _, n := range names {
		s := c.stats[n]
		errs.Samples = append(errs.Samples, Sample{Labels: L("collector", n), Value: float64(s.errors)})
		for i, b := range Buckets {
			dur.Samples = append(dur.Samples, Sample{Suffix: "_bucket", Labels: L("collector", n, "le", formatValue(b)), Value: float64(s.buckets[i])})
		}
		dur.Samples = append(dur.Samples,
			Sample{Suffix: "_bucket", Labels: L("collector", n, "le", "+Inf"), Value: float64(s.count)},
			Sample{Suffix: "_sum", Labels: L("collector", n), Value: s.sum},
			Sample{Suffix: "_count", Labels: L("collector", n), Value: float64(s.count)})
	}
	return []Family{errs, dur}
}
