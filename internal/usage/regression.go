// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package usage

import (
	"errors"

	"github.com/MarioUrenaGarcia/greenlens/internal/stats"
)

// MethodRegression marks values estimated by the regression.
const MethodRegression = "regression"

// MinIntervals is the fewest intervals a regression is reported with.
const MinIntervals = 30

// Observation is one interval of one model.
type Observation struct {
	EnergyJ         float64
	PromptTokens    float64
	GeneratedTokens float64
}

// Regression is the energy of a prompt token and of a generated token of
// one model, from the least squares fit of
//
//	energy = a x prompt tokens + b x generated tokens + c
//
// over its usage intervals, with 95% confidence intervals.
type Regression struct {
	PromptJ       float64 `json:"prompt_j_per_token"`
	PromptCI95    float64 `json:"prompt_ci95"`
	GeneratedJ    float64 `json:"generated_j_per_token"`
	GeneratedCI95 float64 `json:"generated_ci95"`
	// BaseW is the power of the model not explained by tokens, c over the
	// length of an interval.
	BaseW     float64 `json:"base_w"`
	BaseCI95  float64 `json:"base_ci95"`
	R2        float64 `json:"r2"`
	Intervals int     `json:"intervals"`
	Method    string  `json:"method"`
}

// Errors of Regress that say why there is nothing to report.
var (
	ErrFewIntervals = errors.New("too few intervals with tokens")
	ErrPoorFit      = errors.New("tokens explain too little of the energy")
)

// Regress fits the observations. It reports nothing with fewer than
// MinIntervals intervals, when R2 is below minR2, or when a token would
// cost negative energy, which no workload does.
func Regress(obs []Observation, minR2 float64) (Regression, error) {
	if len(obs) < MinIntervals {
		return Regression{}, ErrFewIntervals
	}
	x := make([][]float64, len(obs))
	y := make([]float64, len(obs))
	for i, o := range obs {
		x[i] = []float64{o.PromptTokens, o.GeneratedTokens, 1}
		y[i] = o.EnergyJ
	}
	f, err := stats.LeastSquares(x, y)
	if err != nil {
		return Regression{}, err
	}
	if f.R2 < minR2 || f.Coef[0] < 0 || f.Coef[1] < 0 {
		return Regression{}, ErrPoorFit
	}
	secs := Interval.Seconds()
	return Regression{
		PromptJ: f.Coef[0], PromptCI95: f.CI95[0],
		GeneratedJ: f.Coef[1], GeneratedCI95: f.CI95[1],
		BaseW: f.Coef[2] / secs, BaseCI95: f.CI95[2] / secs,
		R2: f.R2, Intervals: f.N, Method: MethodRegression,
	}, nil
}
