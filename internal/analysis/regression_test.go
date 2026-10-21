// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package analysis

import (
	"math"
	"testing"
	"time"
)

var defaults = Params{BaselineDays: 14, MinBaselineDays: 7, Z: 3, MinTokens: 1000}

// days returns one day per J/token value from start, with 10,000 tokens.
func days(start time.Time, jpt ...float64) []Day {
	out := make([]Day, len(jpt))
	for i, v := range jpt {
		out[i] = Day{Start: start.AddDate(0, 0, i), EnergyJ: v * 10_000, GeneratedTokens: 10_000}
	}
	return out
}

// The case is worked by hand in the explanation of the documentation.
func TestRegressionWorkedCase(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	// Seven baseline days of 0.20, 0.21, 0.19, 0.20, 0.22, 0.18 and 0.20
	// J/token: mean 0.20, sample deviation sqrt(0.0010 / 6) = 0.012910.
	// A day at 0.25 has z = 0.05 / 0.012910 = 3.873, a regression.
	ds := days(start, 0.20, 0.21, 0.19, 0.20, 0.22, 0.18, 0.20, 0.25)
	got := Evaluate(ds, start.AddDate(0, 0, 7), defaults)
	if len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	f := got[0]
	if f.Day != "2026-10-08" || f.BaselineDays != 7 || math.Abs(f.BaselineMean-0.20) > 1e-12 ||
		math.Abs(f.BaselineSD-math.Sqrt(0.001/6)) > 1e-12 || f.Z == nil || math.Abs(*f.Z-0.05/math.Sqrt(0.001/6)) > 1e-9 ||
		f.Status != StatusRegression {
		t.Fatalf("finding %+v", f)
	}
	// 0.21 is z = 0.775, normal; 0.15 is z = -3.873, an improvement.
	for _, tt := range []struct {
		jpt  float64
		want string
	}{{0.21, StatusNormal}, {0.15, StatusImprovement}} {
		ds[7].EnergyJ = tt.jpt * 10_000
		if f := Evaluate(ds, start.AddDate(0, 0, 7), defaults)[0]; f.Status != tt.want {
			t.Errorf("%v: %+v", tt.jpt, f)
		}
	}
}

func TestBaselineNeedsEnoughDays(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	ds := days(start, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.5)
	if f := Evaluate(ds, start, defaults); f[6].Status != StatusInsufficient || f[6].Z != nil || f[6].BaselineDays != 6 {
		t.Fatalf("six days %+v", f[6])
	}
	// Days with too few tokens count neither as days nor in baselines.
	ds = days(start, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.9)
	ds[3].GeneratedTokens = 10
	got := Evaluate(ds, start, defaults)
	if len(got) != 8 || got[7].BaselineDays != 7 {
		t.Fatalf("sparse %+v", got)
	}
	// Only the BaselineDays calendar days before a day form its baseline.
	ds = days(start, 9, 9, 9, 9, 9, 9, 9, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2)
	if f := Evaluate(ds, start.AddDate(0, 0, 21), defaults); f[0].BaselineMean != 0.2 || f[0].BaselineDays != 14 {
		t.Fatalf("window %+v", f[0])
	}
	// A flat baseline uses a deviation of 0.5% of its mean: 0.201 against
	// 0.2 is z = 0.001 / 0.001 = 1, normal.
	ds = days(start, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.2, 0.201)
	if f := Evaluate(ds, start.AddDate(0, 0, 7), defaults)[0]; f.Status != StatusNormal || math.Abs(*f.Z-1) > 1e-9 {
		t.Fatalf("flat %+v", f)
	}
}
