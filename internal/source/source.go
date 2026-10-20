// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package source defines how GreenLens reads energy from the hardware. Each
// source probes what the machine exposes and reads cumulative energy counters
// or instantaneous power for a set of channels; the selector chooses which
// channels to use without counting the same energy twice.
package source

import (
	"context"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
)

// Domain is the part of the system a channel measures.
type Domain string

// Domains. Package, DRAM and GPU are components whose energy adds up to the
// measured node energy. Core and uncore are contained in their package.
// PSys, platform and wall measure levels that already include the components
// and are never added to them.
const (
	DomainPackage  Domain = "package"
	DomainCore     Domain = "core"
	DomainUncore   Domain = "uncore"
	DomainDRAM     Domain = "dram"
	DomainGPU      Domain = "gpu"
	DomainPSys     Domain = "psys"
	DomainPlatform Domain = "platform"
	DomainWall     Domain = "wall"
)

// Level groups domains by how their energy combines.
type Level int

// Levels of a domain.
const (
	// LevelComponent domains are disjoint and add up to the node energy.
	LevelComponent Level = iota
	// LevelContained domains are part of a component and are not added.
	LevelContained
	// LevelSystem domains include several components and are not added.
	LevelSystem
)

// Level returns how the energy of the domain combines with others.
func (d Domain) Level() Level {
	switch d {
	case DomainCore, DomainUncore:
		return LevelContained
	case DomainPSys, DomainPlatform, DomainWall:
		return LevelSystem
	default:
		return LevelComponent
	}
}

// Quality states how directly a value was obtained.
type Quality string

// Qualities, from the most to the least direct.
const (
	QualityMeasured  Quality = "measured"
	QualityPlatform  Quality = "platform"
	QualityEstimated Quality = "estimated"
)

// Method states how an energy value was computed.
type Method string

// Methods used by sources.
const (
	// MethodMeasured comes from a cumulative hardware energy counter.
	MethodMeasured Method = "measured"
	// MethodIntegrated comes from integrating instantaneous power over time.
	MethodIntegrated Method = "integrated"
	// MethodEstimated comes from a power model.
	MethodEstimated Method = "estimated"
)

// Kind is the type of quantity a channel reports.
type Kind int

// Channel kinds.
const (
	// KindCounter channels report cumulative energy in joules.
	KindCounter Kind = iota
	// KindPower channels report instantaneous power in watts.
	KindPower
)

// Channel is one quantity a source can read.
type Channel struct {
	// ID is unique across sources and stable across restarts, for example
	// "rapl:package-0" or "rapl:package-0:dram".
	ID     string `json:"id"`
	Source string `json:"source"`
	Domain Domain `json:"domain"`
	// Instance tells channels of the same domain apart, such as the socket
	// number of a package.
	Instance string  `json:"instance"`
	Quality  Quality `json:"quality"`
	Method   Method  `json:"method"`
	Kind     Kind    `json:"kind"`
	// RangeJ is the value at which a counter wraps to zero, in joules; zero
	// for counters that do not wrap in practice.
	RangeJ float64 `json:"range_j,omitempty"`
	// Label is a short human-readable description of the channel.
	Label string `json:"label,omitempty"`
}

// Reading is one value of a channel. Counters set EnergyJ and power
// channels set PowerW.
type Reading struct {
	Channel string
	EnergyJ float64
	PowerW  float64
}

// Problem explains why a source or one of its channels cannot be used.
type Problem struct {
	// Code identifies the problem and its documented fix.
	Code help.Code `json:"code"`
	// Args are the name/value pairs of the message placeholders.
	Args []any `json:"-"`
	// Channel is set when the problem affects a single channel.
	Channel string `json:"channel,omitempty"`
}

// ProbeResult describes what a source found on this machine.
type ProbeResult struct {
	Source   string    `json:"source"`
	Channels []Channel `json:"channels"`
	Problems []Problem `json:"problems,omitempty"`
}

// Available reports whether the source has at least one usable channel.
func (p ProbeResult) Available() bool { return len(p.Channels) > 0 }

// Source reads energy from one kind of interface.
type Source interface {
	// ID names the source, such as "rapl" or "battery".
	ID() string
	// Priority orders sources that measure the same domain; lower wins.
	Priority() int
	// Probe detects the channels the machine exposes and whether they can
	// be read with the current permissions.
	Probe(ctx context.Context) (ProbeResult, error)
	// Read returns one reading per probed channel that has a value now.
	// A channel without a value, such as a battery while charging, is
	// omitted rather than reported as zero.
	Read(ctx context.Context) ([]Reading, error)
	// Close releases the resources the source opened.
	Close() error
}
