// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package otlp sends the metrics of the agent to an OpenTelemetry collector
// with the OTLP protocol, over gRPC or HTTP. The request is one protobuf
// message with a handful of types, so it is encoded here rather than with
// the generated bindings and the gRPC library, which would add several
// megabytes to a binary that sends one small message every few seconds.
package otlp

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/exporter/prometheus"
)

// ScopeName names the instrumentation scope of the metrics.
const ScopeName = "github.com/MarioUrenaGarcia/greenlens"

// Field numbers of opentelemetry-proto 1.5.0: collector/metrics/v1,
// metrics/v1, resource/v1 and common/v1.
const (
	fRequestResourceMetrics = 1

	fResourceMetricsResource = 1
	fResourceMetricsScope    = 2
	fResourceAttributes      = 1

	fScopeMetricsScope   = 1
	fScopeMetricsMetrics = 2
	fScopeName           = 1
	fScopeVersion        = 2

	fMetricName        = 1
	fMetricDescription = 2
	fMetricUnit        = 3
	fMetricGauge       = 5
	fMetricSum         = 7
	fMetricHistogram   = 9

	// Gauge, Sum and Histogram.
	fDataPoints     = 1
	fTemporality    = 2
	fSumIsMonotonic = 3

	// NumberDataPoint and HistogramDataPoint share the times.
	fPointStart       = 2
	fPointTime        = 3
	fNumberAsDouble   = 4
	fNumberAttributes = 7
	fHistCount        = 4
	fHistSum          = 5
	fHistBucketCounts = 6
	fHistBounds       = 7
	fHistAttributes   = 9

	fKeyValueKey    = 1
	fKeyValueValue  = 2
	fAnyValueString = 1

	// AGGREGATION_TEMPORALITY_CUMULATIVE.
	temporalityCumulative = 2
)

// Parts of the names of the Prometheus histogram samples.
const (
	labelLE      = "le"
	suffixBucket = "_bucket"
	suffixSum    = "_sum"
	suffixCount  = "_count"
)

// units maps the suffix of a metric name to its UCUM unit; the first match
// wins, so the longer suffixes come first.
var units = []struct{ suffix, unit string }{
	{"_joules_per_token", "J/{token}"},
	{"_joules_total", "J"},
	{"_watts", "W"},
	{"_seconds", "s"},
	{"_grams_co2e_per_unit", "g/{unit}"},
	{"_grams_co2e_total", "g"},
	{"_liters_total", "L"},
	{"_tokens_total", "{token}"},
	{"_errors_total", "{error}"},
	{"_info", "1"},
	{"_up", "1"},
	{"_zscore", "1"},
}

// Unit returns the UCUM unit of a metric from the suffix of its name, or
// the empty string when the name states none, as for a cost in a currency.
// The names are those of the Prometheus exposition, so both exports show
// the same series.
func Unit(name string) string {
	for _, u := range units {
		if strings.HasSuffix(name, u.suffix) {
			return u.unit
		}
	}
	return ""
}

// Resource describes the agent that produced the metrics.
type Resource struct {
	// Attributes are name and value pairs, such as service.name.
	Attributes []prometheus.Label
	// Version is the version of the agent, reported as the version of the
	// instrumentation scope.
	Version string
}

// Encode returns an ExportMetricsServiceRequest with the families.
// Counters and histograms are cumulative since start; gauges hold the
// value at now. Families without samples are left out.
func Encode(fams []prometheus.Family, res Resource, start, now time.Time) []byte {
	startNano, nowNano := nanos(start), nanos(now)
	var req buf
	req.msg(fRequestResourceMetrics, func(rm *buf) {
		rm.msg(fResourceMetricsResource, func(r *buf) {
			for _, a := range res.Attributes {
				keyValue(r, fResourceAttributes, a)
			}
		})
		rm.msg(fResourceMetricsScope, func(sm *buf) {
			sm.msg(fScopeMetricsScope, func(s *buf) {
				s.str(fScopeName, ScopeName)
				s.str(fScopeVersion, res.Version)
			})
			for _, f := range fams {
				if len(f.Samples) == 0 {
					continue
				}
				sm.msg(fScopeMetricsMetrics, func(m *buf) { metric(m, f, startNano, nowNano) })
			}
		})
	})
	return req.b
}

func nanos(t time.Time) uint64 {
	n := t.UnixNano()
	if n < 0 {
		return 0
	}
	return uint64(n)
}

func keyValue(w *buf, field int, l prometheus.Label) {
	w.msg(field, func(kv *buf) {
		kv.str(fKeyValueKey, l.Name)
		kv.msg(fKeyValueValue, func(v *buf) {
			// An empty string is written too: an AnyValue without a
			// value would be an empty attribute, not an empty string.
			v.bytes(fAnyValueString, []byte(l.Value))
		})
	})
}

func metric(m *buf, f prometheus.Family, start, now uint64) {
	m.str(fMetricName, f.Name)
	m.str(fMetricDescription, f.Help)
	m.str(fMetricUnit, Unit(f.Name))
	switch f.Type {
	case prometheus.Counter:
		m.msg(fMetricSum, func(s *buf) {
			for _, sm := range f.Samples {
				s.msg(fDataPoints, func(p *buf) { numberPoint(p, sm, start, now) })
			}
			s.varint(fTemporality, temporalityCumulative)
			s.varint(fSumIsMonotonic, 1)
		})
	case prometheus.Histogram:
		m.msg(fMetricHistogram, func(h *buf) {
			for _, hp := range histogramPoints(f.Samples) {
				h.msg(fDataPoints, func(p *buf) {
					p.fixed64(fPointStart, start)
					p.fixed64(fPointTime, now)
					p.fixed64(fHistCount, hp.count)
					p.double(fHistSum, hp.sum)
					p.packedFixed64(fHistBucketCounts, hp.buckets)
					p.packedDouble(fHistBounds, hp.bounds)
					for _, l := range hp.labels {
						keyValue(p, fHistAttributes, l)
					}
				})
			}
			h.varint(fTemporality, temporalityCumulative)
		})
	default:
		m.msg(fMetricGauge, func(g *buf) {
			for _, sm := range f.Samples {
				g.msg(fDataPoints, func(p *buf) { numberPoint(p, sm, 0, now) })
			}
		})
	}
}

func numberPoint(p *buf, s prometheus.Sample, start, now uint64) {
	if start != 0 {
		p.fixed64(fPointStart, start)
	}
	p.fixed64(fPointTime, now)
	p.double(fNumberAsDouble, s.Value)
	for _, l := range s.Labels {
		keyValue(p, fNumberAttributes, l)
	}
}

// histPoint is one histogram series: its buckets hold the observations of
// each interval, not the running totals of the Prometheus exposition.
type histPoint struct {
	labels  []prometheus.Label
	bounds  []float64
	buckets []uint64
	count   uint64
	sum     float64
}

// histogramPoints rebuilds the series of a histogram family from its
// _bucket, _sum and _count samples, in the order they first appear.
func histogramPoints(samples []prometheus.Sample) []histPoint {
	type cumulative struct {
		le    float64
		count uint64
	}
	var keys []string
	points := map[string]*histPoint{}
	cumul := map[string][]cumulative{}
	for _, s := range samples {
		var labels []prometheus.Label
		le := ""
		for _, l := range s.Labels {
			if s.Suffix == suffixBucket && l.Name == labelLE {
				le = l.Value
				continue
			}
			labels = append(labels, l)
		}
		var kb strings.Builder
		for _, l := range labels {
			kb.WriteString(l.Name + "\x00" + l.Value + "\x00")
		}
		key := kb.String()
		hp, ok := points[key]
		if !ok {
			hp = &histPoint{labels: labels}
			points[key] = hp
			keys = append(keys, key)
		}
		switch s.Suffix {
		case suffixBucket:
			bound, err := strconv.ParseFloat(le, 64)
			if err != nil {
				continue
			}
			cumul[key] = append(cumul[key], cumulative{bound, count(s.Value)})
		case suffixSum:
			hp.sum = s.Value
		case suffixCount:
			hp.count = count(s.Value)
		}
	}
	out := make([]histPoint, 0, len(keys))
	for _, key := range keys {
		hp := points[key]
		cs := cumul[key]
		slices.SortFunc(cs, func(a, b cumulative) int {
			switch {
			case a.le < b.le:
				return -1
			case a.le > b.le:
				return 1
			}
			return 0
		})
		var prev uint64
		for _, c := range cs {
			if !math.IsInf(c.le, 1) {
				hp.bounds = append(hp.bounds, c.le)
			}
			hp.buckets = append(hp.buckets, c.count-min(prev, c.count))
			prev = c.count
		}
		// The format needs one bucket more than bounds; a family without
		// the +Inf bucket gets it from the count.
		if len(hp.buckets) == len(hp.bounds) {
			hp.buckets = append(hp.buckets, hp.count-min(prev, hp.count))
		}
		out = append(out, *hp)
	}
	return out
}

func count(v float64) uint64 {
	if v <= 0 || math.IsNaN(v) {
		return 0
	}
	return uint64(v)
}
