// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"cmp"
	"slices"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

// span returns the part of the hour at that falls inside its bucket and
// the period, over which the energy of a row split by hour was used.
func span(hour, bucketStart time.Time, bucket time.Duration, from, to time.Time) (time.Time, time.Time) {
	lo, hi := hour, hour.Add(time.Hour)
	if bucket > 0 {
		lo, hi = later(lo, bucketStart), earlier(hi, bucketStart.Add(bucket))
	}
	return later(lo, from), earlier(hi, to)
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// nodeIndicators adds the indicators to the rows of a node query. Rows
// split by hour are folded into their buckets, each hour with the factors
// of that hour.
func nodeIndicators(rows []store.ChannelEnergy, f indicators.Factors, from, to time.Time, bucket time.Duration, hourly bool) []store.ChannelEnergy {
	if rows == nil {
		rows = []store.ChannelEnergy{}
	}
	if !hourly {
		for i := range rows {
			v := f.Compute(rows[i].EnergyJ)
			rows[i].Indicators = &v
		}
		return rows
	}
	type key struct {
		bucket  time.Time
		channel string
	}
	at := map[key]int{}
	out := []store.ChannelEnergy{}
	for _, r := range rows {
		lo, hi := span(r.Hour, r.BucketStart, bucket, from, to)
		v := f.ComputeOver(r.EnergyJ, lo, hi)
		k := key{r.BucketStart, r.Channel}
		if i, ok := at[k]; ok {
			out[i].EnergyJ += r.EnergyJ
			out[i].Samples += r.Samples
			out[i].Indicators.Add(v)
			continue
		}
		r.Hour, r.Indicators = time.Time{}, &v
		at[k] = len(out)
		out = append(out, r)
	}
	slices.SortStableFunc(out, func(a, b store.ChannelEnergy) int {
		return cmp.Or(a.BucketStart.Compare(b.BucketStart), cmp.Compare(a.Channel, b.Channel))
	})
	return out
}

// usageIndicators does for usage rows what nodeIndicators does for the
// rows of the node.
func usageIndicators(rows []store.UsageEnergy, f indicators.Factors, from, to time.Time, bucket time.Duration, hourly bool) []store.UsageEnergy {
	if rows == nil {
		rows = []store.UsageEnergy{}
	}
	if !hourly {
		for i := range rows {
			v := f.Compute(rows[i].EnergyJ)
			rows[i].Indicators = &v
		}
		return rows
	}
	type key struct {
		bucket                   time.Time
		backend, model, consumer string
	}
	at := map[key]int{}
	out := []store.UsageEnergy{}
	for _, r := range rows {
		start := r.BucketStart
		if bucket <= 0 {
			start = from
		}
		lo, hi := span(r.Hour, start, bucket, from, to)
		v := f.ComputeOver(r.EnergyJ, lo, hi)
		k := key{r.BucketStart, r.Backend, r.Model, r.Consumer}
		if i, ok := at[k]; ok {
			o := &out[i]
			o.EnergyJ += r.EnergyJ
			o.GPUEnergyJ += r.GPUEnergyJ
			o.PromptTokens += r.PromptTokens
			o.GeneratedTokens += r.GeneratedTokens
			o.Requests += r.Requests
			o.Counted = o.Counted || r.Counted
			o.Indicators.Add(v)
			continue
		}
		r.Hour, r.Indicators = time.Time{}, &v
		at[k] = len(out)
		out = append(out, r)
	}
	slices.SortStableFunc(out, func(a, b store.UsageEnergy) int {
		return cmp.Or(a.BucketStart.Compare(b.BucketStart), cmp.Compare(b.EnergyJ, a.EnergyJ), cmp.Compare(a.Backend, b.Backend))
	})
	return out
}
