// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package registry builds the standard set of energy sources and probes
// them.
package registry

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/battery"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/estimate"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/hwmon"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/ipmi"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/perfevent"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/rapl"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/selector"
)

// Options describes the host and the hooks used to reach it.
type Options struct {
	// FS is the host file system, rooted at HostRoot.
	FS fs.FS
	// HostRoot is the path of the host root, "/" outside containers.
	HostRoot string
	// Perf opens perf_event counters; nil uses the system call.
	Perf perfevent.Opener
	// IPMI opens the IPMI device; nil uses the device node.
	IPMI ipmi.Opener
	// Estimate overrides the coefficients of the CPU model.
	Estimate estimate.Override
}

// Sources returns every source, in the order of the source table: platform
// sources of equal priority are tried in this order.
func Sources(o Options) []source.Source {
	if o.Perf == nil {
		o.Perf = perfevent.SystemOpener{}
	}
	if o.IPMI == nil {
		o.IPMI = ipmi.SystemOpener{}
	}
	if o.HostRoot == "" {
		o.HostRoot = "/"
	}
	return []source.Source{
		rapl.New(o.FS),
		perfevent.New(o.FS, o.Perf),
		hwmon.NewCPU(o.FS),
		hwmon.NewACPI(o.FS),
		ipmi.New(o.HostRoot, o.IPMI),
		battery.New(o.FS),
		estimate.New(o.FS, o.Estimate),
	}
}

// Probe probes every source. A source that fails to probe is reported as
// an error and left out; the others are still returned.
func Probe(ctx context.Context, srcs []source.Source) ([]selector.Candidate, error) {
	var out []selector.Candidate
	var errs []error
	for _, s := range srcs {
		res, err := s.Probe(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("probe %s: %w", s.ID(), err))
			continue
		}
		out = append(out, selector.Candidate{Source: s, Result: res})
	}
	return out, errors.Join(errs...)
}

// Close closes every source.
func Close(srcs []source.Source) error {
	var errs []error
	for _, s := range srcs {
		errs = append(errs, s.Close())
	}
	return errors.Join(errs...)
}
