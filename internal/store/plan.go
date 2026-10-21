// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package store

import (
	"strings"
	"time"
)

// A query of a long period reads each part of it from the coarsest data
// that answers it exactly: an hour that falls whole inside one bucket comes
// from the hourly rollups, a minute from the minute rollups, and only the
// rest from the finest data kept. Each rollup is the sum of the rows it
// folds, so the result is the same as from the finest data, while a query
// of 30 days reads about 720 rows per series instead of 43200.

const (
	hourMS   = int64(time.Hour / time.Millisecond)
	minuteMS = int64(time.Minute / time.Millisecond)
	// maxSpans bounds the parts of a plan, each a part of one SQL
	// statement. A period cut in more parts, by buckets that do not line
	// up with hours, is read from its finest data alone.
	maxSpans = 200
)

// span is a range [from, to) of times in milliseconds.
type span struct{ from, to int64 }

func appendSpan(s []span, from, to int64) []span {
	if from >= to {
		return s
	}
	if n := len(s); n > 0 && s[n-1].to == from {
		s[n-1].to = to
		return s
	}
	return append(s, span{from, to})
}

func ceilTo(v, m int64) int64 { return (v + m - 1) / m * m }

// readPlan names the ranges read from each resolution. Rows are taken by
// their start time.
type readPlan struct {
	hours, minutes, raw []span
}

// planRead splits [from, to) for a query whose finest data is finest.
// done1m and done1h are the ends of the folded minutes and hours, and
// bucket the size of the buckets counted from from, or zero for one total.
func planRead(from, to, done1m, done1h, bucket int64, finest string) readPlan {
	var p readPlan
	if finest == Resolution1h {
		p.hours = appendSpan(nil, from, min(to, done1h))
		p.raw = appendSpan(nil, max(from, done1h), to)
		return p
	}
	b := bucket
	if b <= 0 {
		b = to - from + 1
	}
	whole := func(start, size int64) bool { return (start-from)/b == (start+size-1-from)/b }
	var gaps []span
	cur := from
	for h := ceilTo(from, hourMS); h+hourMS <= min(to, done1h); h += hourMS {
		if whole(h, hourMS) {
			gaps = appendSpan(gaps, cur, h)
			p.hours = appendSpan(p.hours, h, h+hourMS)
			cur = h + hourMS
		}
	}
	gaps = appendSpan(gaps, cur, to)
	for _, g := range gaps {
		if finest == Resolution1m {
			// Without raw rows, a minute is read whole even where it
			// crosses the edge of the period or of a bucket.
			p.minutes = appendSpan(p.minutes, g.from, min(g.to, done1m))
			p.raw = appendSpan(p.raw, max(g.from, done1m), g.to)
			continue
		}
		cur := g.from
		for m := ceilTo(g.from, minuteMS); m+minuteMS <= min(g.to, done1m); m += minuteMS {
			if whole(m, minuteMS) {
				p.raw = appendSpan(p.raw, cur, m)
				p.minutes = appendSpan(p.minutes, m, m+minuteMS)
				cur = m + minuteMS
			}
		}
		p.raw = appendSpan(p.raw, cur, g.to)
	}
	if len(p.hours)+len(p.minutes)+len(p.raw) > maxSpans {
		p = readPlan{}
		if finest == Resolution1m {
			p.minutes = appendSpan(nil, from, min(to, done1m))
			p.raw = appendSpan(nil, max(from, done1m), to)
		} else {
			p.raw = appendSpan(nil, from, to)
		}
	}
	return p
}

// union joins one select per range: rollup reads a resolution and a range
// of bucket starts, raw a range of row times. Every value is a placeholder.
func (p readPlan) union(rollup, raw string) (string, []any) {
	var parts []string
	var args []any
	for _, part := range []struct {
		res   string
		spans []span
	}{{Resolution1h, p.hours}, {Resolution1m, p.minutes}} {
		for _, s := range part.spans {
			parts = append(parts, rollup)
			args = append(args, part.res, s.from, s.to)
		}
	}
	for _, s := range p.raw {
		parts = append(parts, raw)
		args = append(args, s.from, s.to)
	}
	if len(parts) == 0 {
		// An empty period still needs a select with the right columns.
		parts, args = []string{raw}, []any{int64(0), int64(0)}
	}
	return strings.Join(parts, " UNION ALL "), args
}

// finest returns the finest resolution kept at from.
func finest(from, now time.Time, r Retention) string {
	switch {
	case !from.Before(now.Add(-r.Raw)):
		return ResolutionRaw
	case !from.Before(now.Add(-r.Rollup1m)):
		return Resolution1m
	}
	return Resolution1h
}
