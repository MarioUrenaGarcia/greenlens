// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package battery measures the power of a laptop while it runs on battery,
// from /sys/class/power_supply. While the charger is connected the battery
// does not see the power drawn by the system, so no value is reported.
package battery

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path"

	"github.com/MarioUrenaGarcia/greenlens/internal/hostfs"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

// ID names this source.
const ID = "battery"

const supplyDir = "sys/class/power_supply"

// Source reads the batteries of the machine.
type Source struct {
	fsys      fs.FS
	batteries []battery
}

type battery struct {
	dir     string
	channel source.Channel
}

// New returns a source that reads the host file system fsys.
func New(fsys fs.FS) *Source { return &Source{fsys: fsys} }

// ID implements source.Source.
func (s *Source) ID() string { return ID }

// Priority implements source.Source.
func (s *Source) Priority() int { return 4 }

// Close implements source.Source.
func (s *Source) Close() error { return nil }

// Probe implements source.Source.
func (s *Source) Probe(_ context.Context) (source.ProbeResult, error) {
	res := source.ProbeResult{Source: ID}
	s.batteries = nil
	supplies, err := hostfs.Dirs(s.fsys, supplyDir)
	if err != nil {
		return res, err
	}
	for _, name := range supplies {
		dir := path.Join(supplyDir, name)
		if t, _ := hostfs.String(s.fsys, path.Join(dir, "type")); t != "Battery" {
			continue
		}
		if !s.measuresPower(dir) {
			continue
		}
		ch := source.Channel{
			ID:       ID + ":" + name,
			Source:   ID,
			Domain:   source.DomainPlatform,
			Instance: name,
			Quality:  source.QualityPlatform,
			Method:   source.MethodIntegrated,
			Kind:     source.KindPower,
			Label:    name,
		}
		s.batteries = append(s.batteries, battery{dir: dir, channel: ch})
		res.Channels = append(res.Channels, ch)
	}
	return res, nil
}

// measuresPower reports whether the battery exposes power, or current and
// voltage to compute it.
func (s *Source) measuresPower(dir string) bool {
	has := func(f string) bool { return hostfs.Exists(s.fsys, path.Join(dir, f)) }
	return has("power_now") || (has("current_now") && has("voltage_now"))
}

// Read implements source.Source. Batteries that are not discharging are
// left out of the result.
func (s *Source) Read(_ context.Context) ([]source.Reading, error) {
	var out []source.Reading
	var errs []error
	for _, b := range s.batteries {
		status, err := hostfs.String(s.fsys, path.Join(b.dir, "status"))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", b.channel.ID, err))
			continue
		}
		if status != "Discharging" {
			continue
		}
		w, err := s.power(b.dir)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", b.channel.ID, err))
			continue
		}
		out = append(out, source.Reading{Channel: b.channel.ID, PowerW: w})
	}
	return out, errors.Join(errs...)
}

// power prefers power_now (microwatts); otherwise it multiplies current_now
// (microamps) by voltage_now (microvolts). Some drivers report the current
// as negative while discharging, so only its magnitude is used.
func (s *Source) power(dir string) (float64, error) {
	if uw, err := hostfs.Int64(s.fsys, path.Join(dir, "power_now")); err == nil {
		return math.Abs(float64(uw)) / 1e6, nil
	}
	ua, err := hostfs.Int64(s.fsys, path.Join(dir, "current_now"))
	if err != nil {
		return 0, err
	}
	uv, err := hostfs.Int64(s.fsys, path.Join(dir, "voltage_now"))
	if err != nil {
		return 0, err
	}
	return math.Abs(float64(ua)) * float64(uv) / 1e12, nil
}
