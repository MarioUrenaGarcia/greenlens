// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package stats has the statistics GreenLens reports: summaries with
// Student's t confidence intervals, and least squares regression.
package stats

import (
	"errors"
	"math"
)

// Mean returns the arithmetic mean, NaN for no values.
func Mean(v []float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

// StdDev returns the sample standard deviation, NaN for fewer than two
// values.
func StdDev(v []float64) float64 {
	if len(v) < 2 {
		return math.NaN()
	}
	m := Mean(v)
	s := 0.0
	for _, x := range v {
		s += (x - m) * (x - m)
	}
	return math.Sqrt(s / float64(len(v)-1))
}

// Summary describes repeated measurements of one quantity.
type Summary struct {
	N      int     `json:"n"`
	Mean   float64 `json:"mean"`
	StdDev float64 `json:"stddev"`
	// CV is the coefficient of variation, StdDev / Mean.
	CV float64 `json:"cv"`
	// CI95 is the half width of the 95% confidence interval of the mean.
	CI95 float64 `json:"ci95"`
}

// Summarize returns the summary of v, which needs at least two values.
func Summarize(v []float64) (Summary, error) {
	if len(v) < 2 {
		return Summary{}, errors.New("a summary needs at least two values")
	}
	s := Summary{N: len(v), Mean: Mean(v), StdDev: StdDev(v)}
	if s.Mean != 0 {
		s.CV = s.StdDev / math.Abs(s.Mean)
	}
	s.CI95 = TQuantile(0.975, float64(len(v)-1)) * s.StdDev / math.Sqrt(float64(len(v)))
	return s, nil
}

// TQuantile returns the p quantile of Student's t distribution with df
// degrees of freedom, found by bisection on its distribution function.
func TQuantile(p, df float64) float64 {
	switch {
	case p <= 0 || p >= 1 || df <= 0:
		return math.NaN()
	case p == 0.5:
		return 0
	case p < 0.5:
		return -TQuantile(1-p, df)
	}
	lo, hi := 0.0, 1.0
	for TCDF(hi, df) < p {
		hi *= 2
		if hi > 1e12 {
			return math.Inf(1)
		}
	}
	for range 200 {
		mid := (lo + hi) / 2
		if TCDF(mid, df) < p {
			lo = mid
		} else {
			hi = mid
		}
		if hi-lo < 1e-12*math.Max(1, hi) {
			break
		}
	}
	return (lo + hi) / 2
}

// TCDF is the distribution function of Student's t with df degrees of
// freedom: 1 - I_x(df/2, 1/2) / 2 for t > 0, with x = df / (df + t^2).
func TCDF(t, df float64) float64 {
	x := df / (df + t*t)
	tail := 0.5 * regIncBeta(df/2, 0.5, x)
	if t > 0 {
		return 1 - tail
	}
	return tail
}

// regIncBeta is the regularized incomplete beta function I_x(a, b),
// evaluated with the continued fraction of Lentz's method, on the side of
// x where it converges fast.
func regIncBeta(a, b, x float64) float64 {
	switch {
	case x <= 0:
		return 0
	case x >= 1:
		return 1
	}
	la, _ := math.Lgamma(a + b)
	lb, _ := math.Lgamma(a)
	lc, _ := math.Lgamma(b)
	front := math.Exp(la - lb - lc + a*math.Log(x) + b*math.Log(1-x))
	if x < (a+1)/(a+b+2) {
		return front * betaFraction(a, b, x) / a
	}
	return 1 - front*betaFraction(b, a, 1-x)/b
}

func betaFraction(a, b, x float64) float64 {
	const tiny = 1e-300
	c, d := 1.0, 1-(a+b)*x/(a+1)
	if math.Abs(d) < tiny {
		d = tiny
	}
	d = 1 / d
	f := d
	for m := 1; m <= 300; m++ {
		fm := float64(m)
		// Even step.
		num := fm * (b - fm) * x / ((a + 2*fm - 1) * (a + 2*fm))
		d = 1 + num*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + num/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		f *= d * c
		// Odd step.
		num = -(a + fm) * (a + b + fm) * x / ((a + 2*fm) * (a + 2*fm + 1))
		d = 1 + num*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + num/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		delta := d * c
		f *= delta
		if math.Abs(delta-1) < 1e-15 {
			break
		}
	}
	return f
}

// Fit is a least squares fit y = X b.
type Fit struct {
	// Coef are the coefficients, in the order of the columns of X.
	Coef []float64
	// CI95 are the half widths of their 95% confidence intervals.
	CI95 []float64
	// R2 is the coefficient of determination.
	R2 float64
	N  int
}

// ErrSingular means the columns of X are linearly dependent, as when one
// token count is always zero or proportional to the other.
var ErrSingular = errors.New("the regressors are linearly dependent")

// LeastSquares fits y = X b by ordinary least squares, solving the normal
// equations. X has one row per observation; there must be more rows than
// columns.
func LeastSquares(x [][]float64, y []float64) (Fit, error) {
	n := len(y)
	if n == 0 || len(x) != n {
		return Fit{}, errors.New("one row of regressors per observation is needed")
	}
	k := len(x[0])
	if n <= k {
		return Fit{}, errors.New("more observations than coefficients are needed")
	}
	// Normal equations, augmented with the identity to invert them.
	a := make([][]float64, k)
	for i := range a {
		a[i] = make([]float64, 2*k+1)
		a[i][k+i] = 1
	}
	for r, row := range x {
		if len(row) != k {
			return Fit{}, errors.New("every row needs the same number of regressors")
		}
		for i := range k {
			for j := range k {
				a[i][j] += row[i] * row[j]
			}
			a[i][2*k] += row[i] * y[r]
		}
	}
	scale := 0.0
	for i := range k {
		scale = math.Max(scale, math.Abs(a[i][i]))
	}
	for col := range k {
		pivot := col
		for r := col + 1; r < k; r++ {
			if math.Abs(a[r][col]) > math.Abs(a[pivot][col]) {
				pivot = r
			}
		}
		if math.Abs(a[pivot][col]) <= 1e-12*scale {
			return Fit{}, ErrSingular
		}
		a[col], a[pivot] = a[pivot], a[col]
		p := a[col][col]
		for j := range a[col] {
			a[col][j] /= p
		}
		for r := range k {
			if r == col || a[r][col] == 0 {
				continue
			}
			f := a[r][col]
			for j := range a[r] {
				a[r][j] -= f * a[col][j]
			}
		}
	}
	fit := Fit{Coef: make([]float64, k), CI95: make([]float64, k), N: n}
	for i := range k {
		fit.Coef[i] = a[i][2*k]
	}
	my := Mean(y)
	var rss, tss float64
	for r, row := range x {
		pred := 0.0
		for i, v := range row {
			pred += fit.Coef[i] * v
		}
		rss += (y[r] - pred) * (y[r] - pred)
		tss += (y[r] - my) * (y[r] - my)
	}
	if tss > 0 {
		fit.R2 = 1 - rss/tss
	}
	df := float64(n - k)
	t := TQuantile(0.975, df)
	s2 := rss / df
	for i := range k {
		fit.CI95[i] = t * math.Sqrt(math.Max(0, s2*a[i][k+i]))
	}
	return fit, nil
}
