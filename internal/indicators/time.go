// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package indicators

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// Profile is the carbon intensity of the grid in each hour of the week, in
// gCO2e/kWh, by weekday (time.Sunday is 0) and hour of the local day.
type Profile [7][24]float64

// ProfileHeader is the first line of a profile file.
const ProfileHeader = "weekday,hour,g_co2e_per_kwh"

// ParseProfile reads a profile in CSV: the header, then one line per hour
// of the week with the ISO weekday (1 is Monday, 7 is Sunday), the hour
// from 0 to 23 and the intensity. Empty lines and lines that start with #
// are skipped. Every hour must appear exactly once.
func ParseProfile(r io.Reader) (*Profile, error) {
	var p Profile
	var seen [7][24]bool
	sc := bufio.NewScanner(r)
	line, header := 0, false
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if !header {
			if strings.ReplaceAll(text, " ", "") != ProfileHeader {
				return nil, fmt.Errorf("line %d: the first line must be %q", line, ProfileHeader)
			}
			header = true
			continue
		}
		fields := strings.Split(text, ",")
		if len(fields) != 3 {
			return nil, fmt.Errorf("line %d: want 3 fields, found %d", line, len(fields))
		}
		day, err1 := strconv.Atoi(strings.TrimSpace(fields[0]))
		hour, err2 := strconv.Atoi(strings.TrimSpace(fields[1]))
		g, err3 := strconv.ParseFloat(strings.TrimSpace(fields[2]), 64)
		switch {
		case err1 != nil || day < 1 || day > 7:
			return nil, fmt.Errorf("line %d: weekday must be 1 (Monday) to 7 (Sunday)", line)
		case err2 != nil || hour < 0 || hour > 23:
			return nil, fmt.Errorf("line %d: hour must be 0 to 23", line)
		case err3 != nil || g < 0 || math.IsNaN(g) || math.IsInf(g, 0):
			return nil, fmt.Errorf("line %d: the intensity must be a number of gCO2e/kWh, zero or more", line)
		}
		wd := day % 7
		if seen[wd][hour] {
			return nil, fmt.Errorf("line %d: weekday %d, hour %d appears twice", line, day, hour)
		}
		seen[wd][hour] = true
		p[wd][hour] = g
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !header {
		return nil, fmt.Errorf("the file is empty; the first line must be %q", ProfileHeader)
	}
	for wd := range seen {
		for h, ok := range seen[wd] {
			if !ok {
				return nil, fmt.Errorf("weekday %d, hour %d is missing; the profile needs all 168 hours of the week", (wd+6)%7+1, h)
			}
		}
	}
	return &p, nil
}

// ReadProfile reads a profile file.
func ReadProfile(path string) (*Profile, error) {
	f, err := os.Open(path) //nolint:gosec // the path comes from the configuration
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return ParseProfile(f)
}

// Period is a time-of-use price of electricity on some weekdays, from one
// local clock time to another of the same day.
type Period struct {
	Days [7]bool
	// From and To are minutes after local midnight; To may be 1440.
	From, To    int
	PricePerKWh float64
}

// Weekdays are the names of the days in tariff periods.
var Weekdays = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// ParseClock reads a time of day such as "18:00", or "24:00" for the end of
// the day, as minutes after midnight.
func ParseClock(s string) (int, error) {
	hh, mm, ok := strings.Cut(s, ":")
	h, err1 := strconv.Atoi(hh)
	m, err2 := strconv.Atoi(mm)
	if !ok || len(hh) != 2 || len(mm) != 2 || err1 != nil || err2 != nil || h < 0 || m < 0 || m > 59 || h > 24 || (h == 24 && m != 0) {
		return 0, fmt.Errorf("%q is not a time of day such as 18:00", s)
	}
	return h*60 + m, nil
}

// CheckPeriods reports overlapping periods and, without a base price, the
// first time of the week that no period covers.
func CheckPeriods(ps []Period, base *float64) error {
	if len(ps) == 0 {
		return nil
	}
	var errs []error
	var owner [7][1440]int
	for i, p := range ps {
		if p.From >= p.To {
			errs = append(errs, fmt.Errorf("period %d: from must be before to", i+1))
			continue
		}
		if p.PricePerKWh < 0 || math.IsNaN(p.PricePerKWh) || math.IsInf(p.PricePerKWh, 0) {
			errs = append(errs, fmt.Errorf("period %d: the price must be zero or more", i+1))
		}
		for d, on := range p.Days {
			if !on {
				continue
			}
			for m := p.From; m < p.To; m++ {
				if o := owner[d][m]; o != 0 {
					errs = append(errs, fmt.Errorf("periods %d and %d overlap on %s at %02d:%02d", o, i+1, Weekdays[d], m/60, m%60))
					break
				}
				owner[d][m] = i + 1
			}
		}
	}
	if base == nil && len(errs) == 0 {
	gap:
		for d := range owner {
			for m, o := range owner[d] {
				if o == 0 {
					errs = append(errs, fmt.Errorf("no period covers %s at %02d:%02d; set price_per_kwh for the times outside the periods", Weekdays[d], m/60, m%60))
					break gap
				}
			}
		}
	}
	return errors.Join(errs...)
}

// Varies reports whether the factors change with the time of day, so the
// indicators of energy depend on when it was used.
func (f Factors) Varies() bool { return f.Profile != nil || len(f.Periods) > 0 }

func (f Factors) location() *time.Location {
	if f.Location != nil {
		return f.Location
	}
	return time.UTC
}

// at returns the intensity and the price in force at a local time; the
// price is nil without a tariff.
func (f Factors) at(t time.Time) (float64, *float64) {
	g := f.EmissionGPerKWh
	if f.Profile != nil {
		g = f.Profile[t.Weekday()][t.Hour()]
	}
	minute := t.Hour()*60 + t.Minute()
	for i := range f.Periods {
		p := &f.Periods[i]
		if p.Days[t.Weekday()] && minute >= p.From && minute < p.To {
			return g, &p.PricePerKWh
		}
	}
	return g, f.TariffPerKWh
}

// next returns the first time after t at which the intensity or the price
// may change: the next local hour, or the start or end of a period.
func (f Factors) next(t time.Time) time.Time {
	loc := f.location()
	y, mo, d := t.Date()
	n := time.Date(y, mo, d, t.Hour()+1, 0, 0, 0, loc)
	for _, p := range f.Periods {
		for _, m := range []int{p.From, p.To} {
			for day := 0; day <= 1; day++ {
				c := time.Date(y, mo, d+day, 0, m, 0, 0, loc)
				if c.After(t) && c.Before(n) {
					n = c
				}
			}
		}
	}
	return n
}

// ComputeOver returns the indicators of energy used evenly over [from, to),
// with the intensity and the price of every part of the span. Without
// factors that change with the time, or without a span, it is Compute.
func (f Factors) ComputeOver(joules float64, from, to time.Time) Values {
	if !f.Varies() || !to.After(from) {
		return f.Compute(joules)
	}
	loc := f.location()
	total := to.Sub(from).Seconds()
	var gSum, priceSum float64
	priced := true
	for t := from.In(loc); t.Before(to); {
		end := f.next(t)
		if !end.After(t) {
			// A clock change can make the next local hour ambiguous.
			end = t.Add(time.Hour)
		}
		if end.After(to) {
			end = to.In(loc)
		}
		w := end.Sub(t).Seconds() / total
		g, price := f.at(t)
		gSum += w * g
		if price == nil {
			priced = false
		} else {
			priceSum += w * *price
		}
		t = end
	}
	kwh := joules / JoulesPerKWh
	v := Values{KWh: kwh, CO2eG: kwh * gSum * f.PUE}
	if priced && (f.TariffPerKWh != nil || len(f.Periods) > 0) {
		c := kwh * priceSum * f.PUE
		v.Cost = &c
	}
	if f.WUELPerKWh != nil {
		w := kwh * *f.WUELPerKWh
		v.WaterL = &w
	}
	return v
}

// Add sums the indicators of o into v. A cost or a water use missing on
// either side stays missing only when it is missing on both.
func (v *Values) Add(o Values) {
	v.KWh += o.KWh
	v.CO2eG += o.CO2eG
	v.Cost = addPtr(v.Cost, o.Cost)
	v.WaterL = addPtr(v.WaterL, o.WaterL)
}

func addPtr(a, b *float64) *float64 {
	switch {
	case a == nil && b == nil:
		return nil
	case a == nil:
		c := *b
		return &c
	case b == nil:
		c := *a
		return &c
	}
	c := *a + *b
	return &c
}
