// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package indicators

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// flatProfile returns a profile of g in every hour, with some hours set.
func flatProfile(g float64, set map[[2]int]float64) *Profile {
	var p Profile
	for d := range p {
		for h := range p[d] {
			p[d][h] = g
		}
	}
	for k, v := range set {
		p[k[0]][k[1]] = v
	}
	return &p
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9*math.Max(1, math.Abs(b)) }

func TestComputeOverAProfile(t *testing.T) {
	mx, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		t.Fatal(err)
	}
	f := Default()
	f.PUE = 1.2
	f.Location = mx
	f.Profile = flatProfile(444, map[[2]int]float64{{int(time.Monday), 10}: 400, {int(time.Monday), 11}: 500})
	// Monday 2026-10-19, 10:00 to 12:00 in Mexico City (UTC-6). Two kWh
	// spread evenly put 1 kWh in each hour: (1 x 400 + 1 x 500) x 1.2 = 1080 g.
	from := time.Date(2026, 10, 19, 10, 0, 0, 0, mx)
	v := f.ComputeOver(2*JoulesPerKWh, from, from.Add(2*time.Hour))
	if !near(v.CO2eG, 1080) || !near(v.KWh, 2) {
		t.Fatalf("two hours: %+v", v)
	}
	// 10:30 to 11:30: half a kWh in each hour, (0.5 x 400 + 0.5 x 500) x 1.2 = 540 g.
	v = f.ComputeOver(JoulesPerKWh, from.Add(30*time.Minute), from.Add(90*time.Minute))
	if !near(v.CO2eG, 540) {
		t.Fatalf("half hours: %+v", v)
	}
	// The same instants given in UTC fall in the same local hours.
	v = f.ComputeOver(2*JoulesPerKWh, from.UTC(), from.Add(2*time.Hour).UTC())
	if !near(v.CO2eG, 1080) {
		t.Fatalf("UTC instants: %+v", v)
	}
	// In UTC the profile would apply at 16:00 and 17:00, where it is 444.
	utc := f
	utc.Location = time.UTC
	if v := utc.ComputeOver(2*JoulesPerKWh, from, from.Add(2*time.Hour)); !near(v.CO2eG, 2*444*1.2) {
		t.Fatalf("UTC zone: %+v", v)
	}
	// Without a span, the flat factor applies.
	if v := f.ComputeOver(JoulesPerKWh, from, from); !near(v.CO2eG, 444*1.2) {
		t.Fatalf("no span: %+v", v)
	}
}

func TestComputeOverTariffPeriods(t *testing.T) {
	base := 1.0
	f := Default()
	f.Location = time.UTC
	f.TariffPerKWh = &base
	f.Periods = []Period{{Days: [7]bool{false, true, true, true, true, true, false}, From: 18 * 60, To: 22 * 60, PricePerKWh: 3}}
	// Monday 17:00 to 21:00, 4 kWh evenly: 1 kWh at the base price and
	// 3 kWh in the period, 1 x 1.0 + 3 x 3.0 = 10.
	from := time.Date(2026, 10, 19, 17, 0, 0, 0, time.UTC)
	v := f.ComputeOver(4*JoulesPerKWh, from, from.Add(4*time.Hour))
	if v.Cost == nil || !near(*v.Cost, 10) {
		t.Fatalf("weekday: %+v", v)
	}
	// Saturday has no period: 4 kWh x 1.0 = 4.
	sat := time.Date(2026, 10, 24, 17, 0, 0, 0, time.UTC)
	if v := f.ComputeOver(4*JoulesPerKWh, sat, sat.Add(4*time.Hour)); v.Cost == nil || !near(*v.Cost, 4) {
		t.Fatalf("saturday: %+v", v)
	}
	// A period that ends at 18:20 splits the hour: 20 minutes at 3.0 and
	// 40 at 1.0, for 1 kWh: (20 x 3 + 40 x 1) / 60 = 1.6667.
	f.Periods[0].To = 18*60 + 20
	h := time.Date(2026, 10, 19, 18, 0, 0, 0, time.UTC)
	if v := f.ComputeOver(JoulesPerKWh, h, h.Add(time.Hour)); v.Cost == nil || !near(*v.Cost, 100.0/60) {
		t.Fatalf("partial period: %+v", v)
	}
	// Without any tariff there is no cost.
	f.TariffPerKWh, f.Periods = nil, nil
	f.Profile = flatProfile(444, nil)
	if v := f.ComputeOver(JoulesPerKWh, h, h.Add(time.Hour)); v.Cost != nil {
		t.Fatalf("cost without a tariff: %+v", v)
	}
}

func TestComputeOverAClockChange(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	f := Default()
	f.Location = ny
	f.Profile = flatProfile(0, map[[2]int]float64{{int(time.Sunday), 1}: 100, {int(time.Sunday), 3}: 300})
	// On 2026-03-08 the clocks go from 02:00 to 03:00, so 01:00 to 04:00
	// lasts two hours: 1 kWh in hour 1 and 1 kWh in hour 3, 100 + 300 = 400 g.
	from := time.Date(2026, 3, 8, 1, 0, 0, 0, ny)
	to := time.Date(2026, 3, 8, 4, 0, 0, 0, ny)
	if to.Sub(from) != 2*time.Hour {
		t.Fatalf("span %s", to.Sub(from))
	}
	if v := f.ComputeOver(2*JoulesPerKWh, from, to); !near(v.CO2eG, 400) {
		t.Fatalf("spring forward: %+v", v)
	}
}

func profileCSV(skip, dup bool) string {
	var b strings.Builder
	b.WriteString("# hourly intensity\n" + ProfileHeader + "\n")
	for day := 1; day <= 7; day++ {
		for h := range 24 {
			if skip && day == 7 && h == 23 {
				continue
			}
			fmt.Fprintf(&b, "%d,%d,%d\n", day, h, 100*day+h)
		}
	}
	if dup {
		b.WriteString("1,0,5\n")
	}
	return b.String()
}

func TestParseProfile(t *testing.T) {
	p, err := ParseProfile(strings.NewReader(profileCSV(false, false)))
	if err != nil {
		t.Fatal(err)
	}
	// ISO weekday 7 is Sunday, index 0; 1 is Monday.
	if p[time.Sunday][23] != 723 || p[time.Monday][0] != 100 || p[time.Saturday][5] != 605 {
		t.Fatalf("profile %v", p)
	}
	for _, tt := range []struct{ name, in, want string }{
		{"missing hour", profileCSV(true, false), "weekday 7, hour 23 is missing"},
		{"duplicate", profileCSV(false, true), "appears twice"},
		{"header", "day,hour,g\n", "the first line must be"},
		{"empty", "", "the file is empty"},
		{"weekday", ProfileHeader + "\n0,1,2\n", "weekday must be 1"},
		{"hour", ProfileHeader + "\n1,24,2\n", "hour must be 0 to 23"},
		{"negative", ProfileHeader + "\n1,1,-2\n", "zero or more"},
		{"fields", ProfileHeader + "\n1,1\n", "want 3 fields"},
	} {
		if _, err := ParseProfile(strings.NewReader(tt.in)); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
}

func TestPeriods(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want int
		ok   bool
	}{{"00:00", 0, true}, {"18:30", 1110, true}, {"24:00", 1440, true}, {"24:01", 0, false}, {"7:00", 0, false}, {"12:60", 0, false}, {"noon", 0, false}} {
		got, err := ParseClock(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("%q: %d %v", tt.in, got, err)
		}
	}
	week := [7]bool{true, true, true, true, true, true, true}
	base := 1.0
	if err := CheckPeriods(nil, nil); err != nil {
		t.Fatalf("no periods: %v", err)
	}
	if err := CheckPeriods([]Period{{Days: week, From: 0, To: 600, PricePerKWh: 1}, {Days: week, From: 600, To: 1440, PricePerKWh: 2}}, nil); err != nil {
		t.Fatalf("a full week without a base price: %v", err)
	}
	if err := CheckPeriods([]Period{{Days: week, From: 0, To: 600}, {Days: week, From: 599, To: 700}}, &base); err == nil || !strings.Contains(err.Error(), "overlap on sun at 09:59") {
		t.Fatalf("overlap: %v", err)
	}
	if err := CheckPeriods([]Period{{Days: week, From: 60, To: 1440}}, nil); err == nil || !strings.Contains(err.Error(), "no period covers sun at 00:00") {
		t.Fatalf("gap: %v", err)
	}
	if err := CheckPeriods([]Period{{Days: week, From: 600, To: 600}}, &base); err == nil {
		t.Fatal("an empty period passed")
	}
}

func TestAddKeepsMissingValues(t *testing.T) {
	one := 1.0
	v := Values{KWh: 1, CO2eG: 2}
	v.Add(Values{KWh: 1, CO2eG: 3, Cost: &one})
	v.Add(Values{KWh: 1, CO2eG: 4, Cost: &one})
	if v.KWh != 3 || v.CO2eG != 9 || v.Cost == nil || *v.Cost != 2 || v.WaterL != nil {
		t.Fatalf("%+v", v)
	}
}
