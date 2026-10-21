// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package analysis finds regressions of the energy per generated token of
// a model: a day whose J/token lies far from the days before it.
package analysis

import (
	"math"
	"time"
)

// Status of a day.
const (
	StatusNormal       = "normal"
	StatusRegression   = "regression"
	StatusImprovement  = "improvement"
	StatusInsufficient = "insufficient"
)

// Params set the baseline and the threshold.
type Params struct {
	// BaselineDays are the calendar days before a day that form its
	// baseline, of which at least MinBaselineDays must have data.
	BaselineDays    int `json:"baseline_days"`
	MinBaselineDays int `json:"min_baseline_days"`
	// Z is the number of standard deviations from the baseline mean that
	// makes a day a regression, or an improvement below it.
	Z float64 `json:"z_threshold"`
	// MinTokens is the fewest generated tokens for a day to count; fewer
	// make the J/token too uncertain.
	MinTokens int64 `json:"min_generated_tokens"`
}

// minRelativeSD bounds the deviation of the baseline from below, at 0.5% of
// its mean, so that a flat baseline does not turn every small change into
// a regression.
const minRelativeSD = 0.005

// Day is the use of a model in one local day.
type Day struct {
	Start           time.Time
	EnergyJ         float64
	GeneratedTokens int64
}

// Finding is the evaluation of one day.
type Finding struct {
	Day             string  `json:"day"`
	EnergyJ         float64 `json:"energy_j"`
	GeneratedTokens int64   `json:"generated_tokens"`
	JPerToken       float64 `json:"j_per_generated_token"`
	BaselineDays    int     `json:"baseline_days"`
	BaselineMean    float64 `json:"baseline_mean"`
	BaselineSD      float64 `json:"baseline_sd"`
	// Z is absent when the baseline has too few days.
	Z      *float64 `json:"z,omitempty"`
	Status string   `json:"status"`
}

// Evaluate judges every day from from on against the BaselineDays calendar
// days before it. days are in order, one per local day, starting at local
// midnight; days with fewer than MinTokens generated tokens are left out
// of both the findings and the baselines.
func Evaluate(days []Day, from time.Time, p Params) []Finding {
	var usable []Day
	for _, d := range days {
		if d.GeneratedTokens >= p.MinTokens && d.GeneratedTokens > 0 {
			usable = append(usable, d)
		}
	}
	out := []Finding{}
	for i, d := range usable {
		if d.Start.Before(from) {
			continue
		}
		start := d.Start.AddDate(0, 0, -p.BaselineDays)
		var base []float64
		for _, b := range usable[:i] {
			if !b.Start.Before(start) {
				base = append(base, b.EnergyJ/float64(b.GeneratedTokens))
			}
		}
		x := d.EnergyJ / float64(d.GeneratedTokens)
		f := Finding{Day: d.Start.Format(time.DateOnly), EnergyJ: d.EnergyJ, GeneratedTokens: d.GeneratedTokens,
			JPerToken: x, BaselineDays: len(base), Status: StatusInsufficient}
		if len(base) >= max(p.MinBaselineDays, 2) {
			mean, sd := meanSD(base)
			f.BaselineMean, f.BaselineSD = mean, sd
			z := (x - mean) / math.Max(sd, minRelativeSD*mean)
			f.Z = &z
			switch {
			case z >= p.Z:
				f.Status = StatusRegression
			case z <= -p.Z:
				f.Status = StatusImprovement
			default:
				f.Status = StatusNormal
			}
		}
		out = append(out, f)
	}
	return out
}

// meanSD returns the mean and the sample standard deviation.
func meanSD(xs []float64) (float64, float64) {
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(len(xs))
	ss := 0.0
	for _, x := range xs {
		ss += (x - mean) * (x - mean)
	}
	return mean, math.Sqrt(ss / float64(len(xs)-1))
}
