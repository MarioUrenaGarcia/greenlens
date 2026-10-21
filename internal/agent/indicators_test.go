// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"math"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

func TestIndicatorsFollowTheHour(t *testing.T) {
	var p indicators.Profile
	p[time.Monday][10], p[time.Monday][11] = 400, 500
	base := 2.0
	f := indicators.Default()
	f.Location = time.UTC
	f.Profile = &p
	f.TariffPerKWh = &base
	f.Periods = []indicators.Period{{Days: [7]bool{false, true}, From: 11 * 60, To: 12 * 60, PricePerKWh: 5}}
	kwh := indicators.JoulesPerKWh
	// Monday 2026-10-19 from 10:30 to 12:00, in one bucket: half an hour at
	// 10 and a whole hour at 11, 1 kWh each.
	from := time.Date(2026, 10, 19, 10, 30, 0, 0, time.UTC)
	to := from.Add(90 * time.Minute)
	h10, h11 := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC), time.Date(2026, 10, 19, 11, 0, 0, 0, time.UTC)
	rows := nodeIndicators([]store.ChannelEnergy{
		{BucketStart: from, Hour: h10, Channel: "pkg", EnergyJ: kwh},
		{BucketStart: from, Hour: h11, Channel: "pkg", EnergyJ: kwh},
		{BucketStart: from, Hour: h11, Channel: "dram", EnergyJ: kwh},
	}, f, from, to, 0, true)
	if len(rows) != 2 || rows[1].Channel != "pkg" || rows[1].EnergyJ != 2*kwh {
		t.Fatalf("rows %+v", rows)
	}
	// 1 kWh x 400 + 1 kWh x 500 = 900 g; cost 1 x 2.0 + 1 x 5.0 = 7.
	v := rows[1].Indicators
	if math.Abs(v.CO2eG-900) > 1e-9 || v.Cost == nil || math.Abs(*v.Cost-7) > 1e-9 || !rows[1].Hour.IsZero() {
		t.Fatalf("pkg %+v", v)
	}
	if d := rows[0].Indicators; math.Abs(d.CO2eG-500) > 1e-9 || math.Abs(*d.Cost-5) > 1e-9 {
		t.Fatalf("dram %+v", d)
	}
	// Usage rows fold the same way, and keep their tokens.
	us := usageIndicators([]store.UsageEnergy{
		{Hour: h10, Backend: "ollama", Model: "qwen", EnergyJ: kwh, GeneratedTokens: 10, Counted: true},
		{Hour: h11, Backend: "ollama", Model: "qwen", EnergyJ: kwh, GeneratedTokens: 5},
	}, f, from, to, 0, true)
	if len(us) != 1 || us[0].GeneratedTokens != 15 || !us[0].Counted || math.Abs(us[0].Indicators.CO2eG-900) > 1e-9 {
		t.Fatalf("usage %+v", us)
	}
	// Without factors that change, each row takes the flat factors.
	flat := indicators.Default()
	plain := nodeIndicators([]store.ChannelEnergy{{BucketStart: from, Channel: "pkg", EnergyJ: kwh}}, flat, from, to, 0, false)
	if math.Abs(plain[0].Indicators.CO2eG-444) > 1e-9 || plain[0].Indicators.Cost != nil {
		t.Fatalf("flat %+v", plain[0].Indicators)
	}
	if got := nodeIndicators(nil, flat, from, to, 0, false); got == nil {
		t.Fatal("no rows must be an empty list")
	}
}

func TestSpanClipsToTheBucketAndThePeriod(t *testing.T) {
	h := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	from, to := h.Add(-time.Hour), h.Add(10*time.Hour)
	// A bucket of 90 minutes that starts at 09:00 ends at 10:30.
	lo, hi := span(h, from, 90*time.Minute, from, to)
	if !lo.Equal(h) || !hi.Equal(h.Add(30*time.Minute)) {
		t.Fatalf("%s %s", lo, hi)
	}
	// The next one starts at 10:30 and holds the rest of the hour.
	lo, hi = span(h, from.Add(90*time.Minute), 90*time.Minute, from, to)
	if !lo.Equal(h.Add(30*time.Minute)) || !hi.Equal(h.Add(time.Hour)) {
		t.Fatalf("%s %s", lo, hi)
	}
	// The period ends at 10:15.
	lo, hi = span(h, from, 0, from, h.Add(15*time.Minute))
	if !lo.Equal(h) || !hi.Equal(h.Add(15*time.Minute)) {
		t.Fatalf("%s %s", lo, hi)
	}
}
