// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package battery

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/MarioUrenaGarcia/greenlens/internal/source/sourcetest"
)

func TestDischarging(t *testing.T) {
	s := New(sourcetest.Host(t, "laptop-battery"))
	res, err := s.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Channels) != 1 || res.Channels[0].ID != "battery:BAT0" {
		t.Fatalf("channels %+v; the AC adapter is not a battery", res.Channels)
	}
	rs, err := s.Read(context.Background())
	if err != nil || len(rs) != 1 || math.Abs(rs[0].PowerW-9.876543) > 1e-9 {
		t.Fatalf("readings %+v, err %v", rs, err)
	}
}

func batteryDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	b := filepath.Join(dir, "sys", "class", "power_supply", "BAT1")
	if err := os.MkdirAll(b, 0o755); err != nil {
		t.Fatal(err)
	}
	for k, v := range files {
		if err := os.WriteFile(filepath.Join(b, k), []byte(v+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCurrentTimesVoltage(t *testing.T) {
	dir := batteryDir(t, map[string]string{"type": "Battery", "status": "Discharging", "current_now": "-1000000", "voltage_now": "12000000"})
	s := New(os.DirFS(dir))
	if _, err := s.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	rs, err := s.Read(context.Background())
	if err != nil || len(rs) != 1 || math.Abs(rs[0].PowerW-12) > 1e-9 {
		t.Fatalf("readings %+v, err %v", rs, err)
	}
}

func TestChargingHasNoValue(t *testing.T) {
	for _, status := range []string{"Charging", "Full", "Not charging", "Unknown"} {
		dir := batteryDir(t, map[string]string{"type": "Battery", "status": status, "power_now": "5000000"})
		s := New(os.DirFS(dir))
		if _, err := s.Probe(context.Background()); err != nil {
			t.Fatal(err)
		}
		rs, err := s.Read(context.Background())
		if err != nil || len(rs) != 0 {
			t.Errorf("%s: readings %+v, err %v", status, rs, err)
		}
	}
}
