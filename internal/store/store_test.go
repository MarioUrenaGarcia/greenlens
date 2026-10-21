// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package store

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/calibrate"
	"github.com/MarioUrenaGarcia/greenlens/internal/sampler"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

var (
	pkg  = source.Channel{ID: "rapl:package-0", Source: "rapl", Domain: source.DomainPackage, Quality: source.QualityMeasured}
	dram = source.Channel{ID: "rapl:package-0:dram", Source: "rapl", Domain: source.DomainDRAM, Quality: source.QualityMeasured}
	ret  = Retention{Raw: 168 * time.Hour, Rollup1m: 2160 * time.Hour, Rollup1h: 17520 * time.Hour}
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "data", "greenlens.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenCreatesSchemaOnce(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	info, err := os.Stat(s.Path())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("database mode %v, err %v", info.Mode(), err)
	}
	latest, _ := LatestSchema()
	if v, err := s.SchemaVersion(ctx); err != nil || v != latest || latest < 4 {
		t.Fatalf("schema version %d, err %v", v, err)
	}
	tables, err := s.Tables(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"schema_migrations", "sources", "node_samples", "backends", "models", "consumers", "sessions",
		"usage_intervals", "usage_rollups", "calibrations", "benchmarks", "alert_events", "audit_log", "node_rollups", "rollup_state"} {
		if !slices.Contains(tables, want) {
			t.Errorf("table %s missing", want)
		}
	}
	var mode string
	if err := s.DB().QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal mode %q, err %v", mode, err)
	}
	var vacuum int
	if err := s.DB().QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&vacuum); err != nil || vacuum != 2 {
		t.Errorf("auto_vacuum %d, want 2 (incremental)", vacuum)
	}
	_ = s.Close()
	again, err := Open(ctx, s.Path())
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = again.Close() }()
	if err := again.QuickCheck(ctx); err != nil {
		t.Fatal(err)
	}
}

func insert(t *testing.T, s *Store, at time.Time, pkgJ, dramJ float64) {
	t.Helper()
	err := s.InsertSample(context.Background(), sampler.Sample{Time: at, Interval: time.Second, Channels: []sampler.ChannelSample{
		{Channel: pkg.ID, Domain: pkg.Domain, EnergyJ: pkgJ, PowerW: pkgJ},
		{Channel: dram.ID, Domain: dram.Domain, EnergyJ: dramJ, PowerW: dramJ},
	}})
	if err != nil {
		t.Fatal(err)
	}
}

func total(rows []ChannelEnergy, ch string) float64 {
	sum := 0.0
	for _, r := range rows {
		if r.Channel == ch {
			sum += r.EnergyJ
		}
	}
	return sum
}

func TestRollupsKeepTheTotal(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	start := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	if err := s.EnsureChannels(ctx, []source.Channel{pkg, dram}, start); err != nil {
		t.Fatal(err)
	}
	// Three hours of one-second samples at 10 W and 2 W.
	for i := 1; i <= 3*3600; i++ {
		insert(t, s, start.Add(time.Duration(i)*time.Second), 10, 2)
	}
	end := start.Add(3 * time.Hour)
	if err := s.Rollup(ctx, end.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	// A second run with nothing new must not count anything twice.
	if err := s.Rollup(ctx, end.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	from, to := start, end.Add(time.Second)
	for _, now := range []time.Time{
		end.Add(time.Hour),            // raw still kept
		end.Add(10 * 24 * time.Hour),  // only 1m rollups
		end.Add(200 * 24 * time.Hour), // only 1h rollups
	} {
		rows, res, err := s.NodeEnergy(ctx, from, to, 0, now, ret)
		if err != nil {
			t.Fatal(err)
		}
		if got := total(rows, pkg.ID); math.Abs(got-10*3*3600) > 1e-6 {
			t.Errorf("%s: package energy %v, want %v", res, got, 10*3*3600)
		}
		if got := total(rows, dram.ID); math.Abs(got-2*3*3600) > 1e-6 {
			t.Errorf("%s: dram energy %v", res, got)
		}
	}
	rows, _, err := s.NodeEnergy(ctx, from, to, time.Hour, end.Add(time.Hour), ret)
	if err != nil {
		t.Fatal(err)
	}
	buckets := map[time.Time]float64{}
	for _, r := range rows {
		if r.Channel == pkg.ID {
			buckets[r.BucketStart] += r.EnergyJ
		}
	}
	// Samples are stamped at the end of their interval, so the last one
	// opens a fourth bucket.
	if len(buckets) != 4 || math.Abs(buckets[start]-10*3599) > 1e-6 {
		t.Errorf("hourly buckets %v", buckets)
	}
}

func TestPruneKeepsUnfoldedData(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.EnsureChannels(ctx, []source.Channel{pkg, dram}, old); err != nil {
		t.Fatal(err)
	}
	insert(t, s, old.Add(time.Second), 5, 1)
	now := old.Add(30 * 24 * time.Hour)
	// Without a rollup, raw data must survive its retention.
	if n, err := s.Prune(ctx, now, ret); err != nil || n != 0 {
		t.Fatalf("pruned %d before rollup, err %v", n, err)
	}
	if err := s.Rollup(ctx, now); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Prune(ctx, now, ret); err != nil || n != 2 {
		t.Fatalf("pruned %d after rollup, err %v", n, err)
	}
	rows, res, err := s.NodeEnergy(ctx, old, now, 0, now, ret)
	if err != nil || res != Resolution1m || total(rows, pkg.ID) != 5 {
		t.Fatalf("after prune %+v %s %v", rows, res, err)
	}
	if err := s.Vacuum(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownChannelIsRejected(t *testing.T) {
	s := open(t)
	err := s.InsertSample(context.Background(), sampler.Sample{Time: time.Now(), Interval: time.Second,
		Channels: []sampler.ChannelSample{{Channel: "never:recorded", EnergyJ: 1}}})
	if err == nil {
		t.Fatal("sample of an unrecorded channel stored")
	}
}

func TestCalibrationAndAudit(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if _, ok, err := s.LatestCalibration(ctx); ok || err != nil {
		t.Fatalf("empty store returned a calibration: %v", err)
	}
	for i, w := range []float64{30, 25} {
		r := calibrate.Result{StartedAt: time.Unix(int64(1000+i), 0), Duration: time.Minute, Samples: 60,
			Domains: map[source.Domain]calibrate.Stats{source.DomainPackage: {P10: w - 1, P50: w, P90: w + 1}}}
		if err := s.SaveCalibration(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := s.LatestCalibration(ctx)
	if err != nil || !ok || got.Domains[source.DomainPackage].P50 != 25 {
		t.Fatalf("latest %+v %v %v", got, ok, err)
	}
	if err := s.Audit(ctx, "uid:1000", "calibrate", "60s"); err != nil {
		t.Fatal(err)
	}
}
