// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package tui

import (
	"math"
	"strings"
)

// series is one line of a chart, oldest value first.
type series struct {
	name   string
	values []float64
}

// braille dots of a cell, indexed by column (0, 1) and row (0 at the top
// to 3), following the Unicode braille patterns block.
var brailleDots = [2][4]rune{{0x01, 0x02, 0x04, 0x40}, {0x08, 0x10, 0x20, 0x80}}

// chart draws lines on a grid of width by height cells, from 0 at the
// bottom to top. With Unicode every cell holds 2 by 4 braille dots; in
// ASCII one point per cell. Each cell is styled by the last series that
// touched it.
func chart(t theme, lines []series, width, height int, top float64) []string {
	if width < 1 || height < 1 {
		return nil
	}
	dotsX, dotsY := 1, 1
	if t.unicode {
		dotsX, dotsY = 2, 4
	}
	cols, rows := width*dotsX, height*dotsY
	type cell struct {
		bits  rune
		owner int
	}
	grid := make([][]cell, height)
	for i := range grid {
		grid[i] = make([]cell, width)
		for j := range grid[i] {
			grid[i][j].owner = -1
		}
	}
	for si, s := range lines {
		vals := resample(s.values, cols)
		prev := -1
		for x, v := range vals {
			if math.IsNaN(v) {
				prev = -1
				continue
			}
			y := 0
			if top > 0 {
				y = int(math.Round(math.Min(v, top) / top * float64(rows-1)))
			}
			// A vertical stroke joins consecutive points, so steep changes
			// stay visible.
			lo, hi := y, y
			if prev >= 0 {
				lo, hi = min(prev, y), max(prev, y)
			}
			for yy := lo; yy <= hi; yy++ {
				row := rows - 1 - yy
				c := &grid[row/dotsY][x/dotsX]
				if t.unicode {
					c.bits |= brailleDots[x%dotsX][row%dotsY]
				} else {
					c.bits = 1
				}
				c.owner = si
			}
			prev = y
		}
	}
	out := make([]string, height)
	for i, row := range grid {
		var b strings.Builder
		for _, c := range row {
			if c.owner < 0 {
				b.WriteByte(' ')
				continue
			}
			ch := "*"
			if t.unicode {
				ch = string(0x2800 + c.bits)
			}
			b.WriteString(t.domain(lines[c.owner].name).Render(ch))
		}
		out[i] = b.String()
	}
	return out
}

// resample stretches or shrinks values to n points, keeping the newest at
// the right edge; missing older points are NaN.
func resample(values []float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = math.NaN()
	}
	if len(values) == 0 {
		return out
	}
	if len(values) <= n {
		copy(out[n-len(values):], values)
		return out
	}
	// Each point is the mean of the values it covers.
	per := float64(len(values)) / float64(n)
	for i := range n {
		lo, hi := int(float64(i)*per), int(float64(i+1)*per)
		sum := 0.0
		for _, v := range values[lo:hi] {
			sum += v
		}
		out[i] = sum / float64(hi-lo)
	}
	return out
}

// niceTop rounds a maximum up to 1, 2 or 5 times a power of ten, so the
// axis reads well.
func niceTop(v float64) float64 {
	if v <= 0 {
		return 1
	}
	p := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 5, 10} {
		if v <= m*p {
			return m * p
		}
	}
	return 10 * p
}
