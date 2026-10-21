// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package store

import (
	"context"
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/sampler"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/usage"
)

// covers checks that the spans of a plan cover [from, to) once.
func covers(t *testing.T, p readPlan, from, to int64) {
	t.Helper()
	var all []span
	all = append(append(append(all, p.hours...), p.minutes...), p.raw...)
	got := int64(0)
	for i, a := range all {
		got += a.to - a.from
		if a.from < from || a.to > to {
			t.Fatalf("span %v outside [%d, %d)", a, from, to)
		}
		for _, b := range all[i+1:] {
			if a.from < b.to && b.from < a.to {
				t.Fatalf("spans %v and %v overlap", a, b)
			}
		}
	}
	if got != to-from {
		t.Fatalf("spans cover %d ms of %d", got, to-from)
	}
}

func TestPlanRead(t *testing.T) {
	day := 24 * hourMS
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	// Whole days of buckets that start on the hour need only hours.
	p := planRead(base, base+3*day, base+3*day, base+3*day, day, ResolutionRaw)
	if len(p.hours) != 1 || len(p.minutes) != 0 || len(p.raw) != 0 {
		t.Fatalf("aligned days: %+v", p)
	}
	// An unaligned start and a tail not folded yet: raw rows at the edges,
	// minutes up to the first hour and after the last folded one.
	from := base + 7*minuteMS + 12_345
	done1h := base + 2*day + 5*hourMS
	done1m := done1h + 17*minuteMS
	to := done1m + 42_000
	p = planRead(from, to, done1m, done1h, 0, ResolutionRaw)
	covers(t, p, from, to)
	want := readPlan{
		hours:   []span{{base + hourMS, done1h}},
		minutes: []span{{base + 8*minuteMS, base + hourMS}, {done1h, done1m}},
		raw:     []span{{from, base + 8*minuteMS}, {done1m, to}},
	}
	if !equalPlans(p, want) {
		t.Fatalf("unaligned: %+v, want %+v", p, want)
	}
	// Without raw rows, the minutes are read from the start of the period.
	p = planRead(from, to, done1m, done1h, 0, Resolution1m)
	if p.minutes[0].from != from || len(p.raw) != 1 || p.raw[0] != (span{done1m, to}) {
		t.Fatalf("minute finest: %+v", p)
	}
	// Buckets of 90 minutes cut every other hour; those hours come from
	// minutes, and every part stays inside one bucket.
	p = planRead(base, base+day, base+day, base+day, 90*minuteMS, ResolutionRaw)
	covers(t, p, base, base+day)
	for _, h := range p.hours {
		for x := h.from; x < h.to; x += hourMS {
			if (x-base)/(90*minuteMS) != (x+hourMS-1-base)/(90*minuteMS) {
				t.Fatalf("hour %d crosses a bucket", x)
			}
		}
	}
	// Too many parts: everything from the finest data.
	p = planRead(base+1, base+400*day, base+400*day, base+400*day, 2*hourMS, ResolutionRaw)
	if len(p.hours) != 0 || len(p.minutes) != 0 || len(p.raw) != 1 {
		t.Fatalf("fallback: %d hours, %d minutes, %d raw", len(p.hours), len(p.minutes), len(p.raw))
	}
	// Random periods and buckets are always covered once.
	rng := rand.New(rand.NewPCG(1, 2))
	for range 2000 {
		from := base + rng.Int64N(3*day)
		to := from + 1 + rng.Int64N(5*day)
		done1m := from + rng.Int64N(to-from+2*hourMS) - hourMS
		done1h := done1m - rng.Int64N(2*hourMS)
		done1m, done1h = done1m/minuteMS*minuteMS, done1h/hourMS*hourMS
		bucket := []int64{0, minuteMS, 5 * minuteMS, hourMS, 90 * minuteMS, day}[rng.IntN(6)]
		covers(t, planRead(from, to, done1m, done1h, bucket, ResolutionRaw), from, to)
	}
}

func equalPlans(a, b readPlan) bool {
	eq := func(x, y []span) bool {
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			if x[i] != y[i] {
				return false
			}
		}
		return true
	}
	return eq(a.hours, b.hours) && eq(a.minutes, b.minutes) && eq(a.raw, b.raw)
}

// TestQueriesMatchTheRawRows compares node and usage queries over periods
// and buckets of every alignment with sums computed here from the rows
// inserted.
func TestQueriesMatchTheRawRows(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	if err := s.EnsureChannels(ctx, []source.Channel{pkg, dram}, start); err != nil {
		t.Fatal(err)
	}
	info := map[string]BackendInfo{"ollama": {Name: "ollama", Kind: "ollama", Endpoint: "http://127.0.0.1:11434"}}
	rng := rand.New(rand.NewPCG(3, 4))
	type sample struct {
		at   time.Time
		j    float64
		used float64
	}
	var samples []sample
	var rows []usage.Row
	end := start.Add(30 * time.Hour)
	for at := start; at.Before(end); at = at.Add(time.Duration(5000+rng.IntN(4000)) * time.Millisecond) {
		j := 1 + rng.Float64()*50
		samples = append(samples, sample{at: at, j: j})
		if err := s.InsertSample(ctx, sampler.Sample{Time: at, Interval: time.Second, Channels: []sampler.ChannelSample{
			{Channel: pkg.ID, Domain: pkg.Domain, EnergyJ: j, PowerW: j},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	var usageAt []sample
	for at := start; at.Before(end); at = at.Add(usage.Interval) {
		cpu := rng.Float64() * 30
		usageAt = append(usageAt, sample{at: at, used: cpu})
		rows = append(rows, row(at, "ollama", "qwen", cpu, 0, 10, 5, usage.MethodTokenWeighted))
	}
	if err := s.InsertUsage(ctx, rows, info, end); err != nil {
		t.Fatal(err)
	}
	now := end.Add(10 * time.Minute)
	if err := s.Rollup(ctx, now); err != nil {
		t.Fatal(err)
	}
	r := Retention{Raw: 7 * 24 * time.Hour, Rollup1m: 90 * 24 * time.Hour, Rollup1h: 2 * 365 * 24 * time.Hour}
	for i := range 40 {
		// The store keeps milliseconds.
		from := start.Add(time.Duration(rng.Int64N(int64(20 * time.Hour)))).Truncate(time.Millisecond)
		to := from.Add(time.Duration(1 + rng.Int64N(int64(12*time.Hour)))).Truncate(time.Millisecond).Add(time.Millisecond)
		bucket := []time.Duration{0, time.Minute, 7 * time.Minute, time.Hour, 90 * time.Minute, 4 * time.Hour}[i%6]
		key := func(at time.Time) time.Time {
			if bucket == 0 {
				return from.UTC()
			}
			return from.Add(at.Sub(from) / bucket * bucket).UTC()
		}
		want := map[time.Time]float64{}
		for _, x := range samples {
			if !x.at.Before(from) && x.at.Before(to) {
				want[key(x.at)] += x.j
			}
		}
		got, res, err := s.NodeEnergy(ctx, from, to, bucket, now, r)
		if err != nil || res != ResolutionRaw {
			t.Fatalf("node: %s %v", res, err)
		}
		gotJ := map[time.Time]float64{}
		for _, g := range got {
			gotJ[g.BucketStart] += g.EnergyJ
		}
		compare(t, "node", from, to, bucket, gotJ, want)

		// Split by hour, each part holds the samples of its UTC hour.
		type part struct{ bucket, hour time.Time }
		wantH := map[part]float64{}
		for _, x := range samples {
			if !x.at.Before(from) && x.at.Before(to) {
				wantH[part{key(x.at), x.at.Truncate(time.Hour).UTC()}] += x.j
			}
		}
		hourly, _, err := s.NodeEnergyHourly(ctx, from, to, bucket, now, r)
		if err != nil {
			t.Fatal(err)
		}
		gotH := map[part]float64{}
		for _, g := range hourly {
			gotH[part{g.BucketStart, g.Hour}] += g.EnergyJ
		}
		for k, w := range wantH {
			if math.Abs(gotH[k]-w) > 1e-6*math.Max(1, w) {
				t.Fatalf("node by hour [%s, %s) by %s: %v has %v, want %v", from, to, bucket, k, gotH[k], w)
			}
		}
		if len(gotH) != len(wantH) {
			t.Fatalf("node by hour: %d parts, want %d", len(gotH), len(wantH))
		}

		wantU := map[time.Time]float64{}
		for _, x := range usageAt {
			if !x.at.Before(from) && x.at.Before(to) {
				wantU[key(x.at)] += x.used
			}
		}
		gotRows, _, err := s.Usage(ctx, from, to, ByModel, bucket, now, r)
		if err != nil {
			t.Fatal(err)
		}
		gotU := map[time.Time]float64{}
		for _, g := range gotRows {
			k := g.BucketStart
			if bucket == 0 {
				k = from.UTC()
			}
			gotU[k] += g.EnergyJ
		}
		compare(t, "usage", from, to, bucket, gotU, wantU)

		hourRows, _, err := s.UsageHourly(ctx, from, to, ByModel, bucket, now, r)
		if err != nil {
			t.Fatal(err)
		}
		byHour := map[time.Time]float64{}
		for _, g := range hourRows {
			byHour[g.Hour] += g.EnergyJ
		}
		wantByHour := map[time.Time]float64{}
		for _, x := range usageAt {
			if !x.at.Before(from) && x.at.Before(to) {
				wantByHour[x.at.Truncate(time.Hour).UTC()] += x.used
			}
		}
		for k, w := range wantByHour {
			if math.Abs(byHour[k]-w) > 1e-6*math.Max(1, w) {
				t.Fatalf("usage by hour: %s has %v, want %v", k, byHour[k], w)
			}
		}
	}
}

func compare(t *testing.T, what string, from, to time.Time, bucket time.Duration, got, want map[time.Time]float64) {
	t.Helper()
	for k, w := range want {
		if math.Abs(got[k]-w) > 1e-6*math.Max(1, w) {
			t.Fatalf("%s [%s, %s) by %s: bucket %s has %v, want %v", what, from.Format(time.RFC3339Nano), to.Format(time.RFC3339Nano), bucket, k, got[k], w)
		}
	}
	for k, g := range got {
		if _, ok := want[k]; !ok && g != 0 {
			t.Fatalf("%s [%s, %s) by %s: unexpected bucket %s with %v", what, from, to, bucket, k, g)
		}
	}
}
