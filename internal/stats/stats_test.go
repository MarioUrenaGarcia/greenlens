// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package stats

import (
	"errors"
	"math"
	"math/rand/v2"
	"testing"
)

// Quantiles of Student's t from published tables, to three decimals.
func TestTQuantile(t *testing.T) {
	for _, c := range []struct{ p, df, want float64 }{
		{0.975, 1, 12.706},
		{0.975, 4, 2.776},
		{0.975, 10, 2.228},
		{0.975, 30, 2.042},
		{0.975, 1000, 1.962},
		{0.95, 5, 2.015},
		{0.995, 20, 2.845},
	} {
		if got := TQuantile(c.p, c.df); math.Abs(got-c.want) > 5e-4 {
			t.Errorf("t(%v, %v) = %.4f, want %.3f", c.p, c.df, got, c.want)
		}
	}
	if got := TQuantile(0.025, 10); math.Abs(got+2.228) > 5e-4 {
		t.Errorf("lower tail %v", got)
	}
	if !math.IsNaN(TQuantile(1, 3)) || TQuantile(0.5, 3) != 0 {
		t.Error("edges")
	}
	// The distribution function of t with one degree of freedom is that of
	// Cauchy: 1/2 + atan(t)/pi.
	for _, x := range []float64{-3, -0.5, 0.2, 1, 7} {
		if got, want := TCDF(x, 1), 0.5+math.Atan(x)/math.Pi; math.Abs(got-want) > 1e-12 {
			t.Errorf("TCDF(%v, 1) = %v, want %v", x, got, want)
		}
	}
}

func TestSummarize(t *testing.T) {
	s, err := Summarize([]float64{10, 12, 11, 13, 9})
	if err != nil {
		t.Fatal(err)
	}
	// stddev sqrt(2.5); CI t(0.975, 4) x sd / sqrt(5).
	if s.N != 5 || s.Mean != 11 || math.Abs(s.StdDev-math.Sqrt(2.5)) > 1e-12 || math.Abs(s.CI95-2.7764*math.Sqrt(2.5)/math.Sqrt(5)) > 1e-3 || math.Abs(s.CV-math.Sqrt(2.5)/11) > 1e-12 {
		t.Fatalf("%+v", s)
	}
	if _, err := Summarize([]float64{1}); err == nil {
		t.Fatal("one value")
	}
}

func TestLeastSquares(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	var x [][]float64
	var y []float64
	// energy = 0.02 J per prompt token + 0.5 J per generated token + 30 J,
	// with noise.
	for range 400 {
		p, g := float64(rng.IntN(4000)), float64(rng.IntN(600))
		x = append(x, []float64{p, g, 1})
		y = append(y, 0.02*p+0.5*g+30+rng.NormFloat64()*5)
	}
	f, err := LeastSquares(x, y)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []float64{0.02, 0.5, 30} {
		if math.Abs(f.Coef[i]-want) > f.CI95[i]*1.5 || f.CI95[i] <= 0 {
			t.Errorf("coefficient %d = %v +- %v, want %v", i, f.Coef[i], f.CI95[i], want)
		}
	}
	if f.R2 < 0.98 || f.N != 400 {
		t.Errorf("R2 %v n %d", f.R2, f.N)
	}
	// Exact data fits exactly.
	f, err = LeastSquares([][]float64{{1, 1}, {2, 1}, {3, 1}, {4, 1}}, []float64{3, 5, 7, 9})
	if err != nil || math.Abs(f.Coef[0]-2) > 1e-9 || math.Abs(f.Coef[1]-1) > 1e-9 || math.Abs(f.R2-1) > 1e-12 {
		t.Fatalf("%+v %v", f, err)
	}
	// Generated tokens always twice the prompt tokens cannot be told apart.
	if _, err := LeastSquares([][]float64{{1, 2, 1}, {2, 4, 1}, {3, 6, 1}, {5, 10, 1}}, []float64{1, 2, 3, 4}); !errors.Is(err, ErrSingular) {
		t.Fatalf("dependent columns: %v", err)
	}
	if _, err := LeastSquares([][]float64{{1, 1}, {2, 1}}, []float64{1, 2}); err == nil {
		t.Fatal("as many rows as columns")
	}
}
