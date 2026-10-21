// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package units

import "testing"

func TestEnergy(t *testing.T) {
	for _, tt := range []struct {
		j    float64
		want string
	}{
		{0, "0 J"}, {0.000512, "0.51 mJ"}, {0.25, "250 mJ"}, {12.345, "12.3 J"}, {3599, "3599 J"},
		{3600, "1.00 Wh"}, {7200 * 12.5, "25.0 Wh"}, {3.6e6 * 1.234, "1.23 kWh"}, {3.6e9, "1000 kWh"},
	} {
		if got := Energy(tt.j, false); got != tt.want {
			t.Errorf("Energy(%v) = %q, want %q", tt.j, got, tt.want)
		}
	}
	if got := Energy(1234.5, true); got != "1234.5 J" {
		t.Errorf("raw = %q", got)
	}
}

func TestPowerAndMass(t *testing.T) {
	for _, tt := range []struct {
		got, want string
	}{
		{Power(0.004, false), "4.00 mW"}, {Power(16.17, false), "16.2 W"}, {Power(1500, false), "1.50 kW"}, {Power(0, false), "0 W"},
		{CO2e(12, false), "12.0 gCO2e"}, {CO2e(4440, false), "4.44 kgCO2e"}, {CO2e(2.5e6, false), "2.50 tCO2e"},
		{Money(1.23456, "MXN", false), "1.2346 MXN"}, {Power(2.5, true), "2.5 W"},
	} {
		if tt.got != tt.want {
			t.Errorf("got %q, want %q", tt.got, tt.want)
		}
	}
}

func TestBytes(t *testing.T) {
	for _, tt := range []struct {
		got, want string
	}{
		{Bytes(0, false), "0 B"}, {Bytes(1023, false), "1023 B"}, {Bytes(4096, false), "4.00 KiB"},
		{Bytes(5<<20, false), "5.00 MiB"}, {Bytes(300<<30, false), "300 GiB"}, {Bytes(3<<40, false), "3.00 TiB"},
		{Bytes(4096, true), "4096 B"},
	} {
		if tt.got != tt.want {
			t.Errorf("got %q, want %q", tt.got, tt.want)
		}
	}
}
