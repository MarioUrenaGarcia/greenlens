// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package estimate models CPU power from utilization when no energy counter
// is readable, as in virtual machines:
//
//	P = threads x (Pmin + (Pmax - Pmin) x utilization^k)
//
// Pmin and Pmax are per hardware thread and come from the embedded table or
// from the configuration. The result is always reported as estimated.
package estimate

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"math"
	"strconv"
	"strings"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/hostfs"
	"github.com/MarioUrenaGarcia/greenlens/internal/procfs"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

// ID names this source.
const ID = "estimate"

// ChannelID is the single channel of the model.
const ChannelID = ID + ":cpu"

// Override replaces the table. When IdleW and FullW are both set they are
// the power of the whole CPU at 0% and 100% utilization; Exponent defaults
// to 1, a linear model.
type Override struct {
	IdleW    float64
	FullW    float64
	Exponent float64
}

// Source estimates CPU power.
type Source struct {
	fsys     fs.FS
	override Override
	idleW    float64
	fullW    float64
	exponent float64
	last     procfs.CPUTimes
	haveLast bool
	// Model describes the coefficients in use, for diagnostics.
	Model string
}

// New returns a model that reads the host file system fsys.
func New(fsys fs.FS, o Override) *Source { return &Source{fsys: fsys, override: o} }

// ID implements source.Source.
func (s *Source) ID() string { return ID }

// Priority implements source.Source: estimation is the last resort.
func (s *Source) Priority() int { return 6 }

// Close implements source.Source.
func (s *Source) Close() error { return nil }

// Probe implements source.Source.
func (s *Source) Probe(_ context.Context) (source.ProbeResult, error) {
	res := source.ProbeResult{Source: ID}
	threads, err := onlineCPUs(s.fsys)
	if err != nil {
		return res, err
	}
	cpu, err := ReadCPU(s.fsys)
	if err != nil {
		return res, err
	}
	s.exponent = s.override.Exponent
	if s.exponent <= 0 {
		s.exponent = 1
	}
	if s.override.IdleW > 0 && s.override.FullW > 0 {
		s.idleW, s.fullW = s.override.IdleW, s.override.FullW
		s.Model = "configuration"
	} else {
		c, known := Lookup(cpu)
		s.idleW = float64(threads) * c.MinW
		s.fullW = float64(threads) * c.MaxW
		s.Model = fmt.Sprintf("%s, %d threads, %.2f to %.2f W per thread", c.Name, threads, c.MinW, c.MaxW)
		if !known {
			res.Problems = append(res.Problems, source.Problem{Code: help.GL1010, Args: []any{"model", cpu.ModelName}})
		}
	}
	if s.last, err = procfs.SystemCPU(s.fsys); err != nil {
		return res, err
	}
	s.haveLast = true
	res.Channels = append(res.Channels, source.Channel{
		ID:       ChannelID,
		Source:   ID,
		Domain:   source.DomainPackage,
		Instance: "all",
		Quality:  source.QualityEstimated,
		Method:   source.MethodEstimated,
		Kind:     source.KindPower,
		Label:    s.Model,
	})
	return res, nil
}

// Read implements source.Source. Utilization is measured since the
// previous call.
func (s *Source) Read(_ context.Context) ([]source.Reading, error) {
	now, err := procfs.SystemCPU(s.fsys)
	if err != nil {
		return nil, err
	}
	u := 0.0
	if s.haveLast && now.Total() > s.last.Total() {
		u = float64(now.Busy()-s.last.Busy()) / float64(now.Total()-s.last.Total())
	}
	s.last, s.haveLast = now, true
	return []source.Reading{{Channel: ChannelID, PowerW: s.Power(u)}}, nil
}

// Power returns the modeled power at a utilization between 0 and 1.
func (s *Source) Power(u float64) float64 {
	u = math.Min(math.Max(u, 0), 1)
	return s.idleW + (s.fullW-s.idleW)*math.Pow(u, s.exponent)
}

func onlineCPUs(fsys fs.FS) (int, error) {
	s, err := hostfs.String(fsys, "sys/devices/system/cpu/online")
	if err != nil {
		return 0, err
	}
	cpus, err := hostfs.CPUList(s)
	if err != nil {
		return 0, err
	}
	if len(cpus) == 0 {
		return 0, fmt.Errorf("no online CPU")
	}
	return len(cpus), nil
}

// ReadCPU parses the first processor block of proc/cpuinfo.
func ReadCPU(fsys fs.FS) (CPU, error) {
	data, err := fs.ReadFile(fsys, "proc/cpuinfo")
	if err != nil {
		return CPU{}, err
	}
	var c CPU
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" && c.Vendor != "" {
			break
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "vendor_id":
			c.Vendor = v
		case "cpu family":
			c.Family, _ = strconv.Atoi(v)
		case "model":
			c.Model, _ = strconv.Atoi(v)
		case "stepping":
			c.Stepping, _ = strconv.Atoi(v)
		case "model name":
			c.ModelName = v
		}
	}
	return c, sc.Err()
}
