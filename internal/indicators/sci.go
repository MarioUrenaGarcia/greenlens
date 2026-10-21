// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package indicators

import "time"

// The Software Carbon Intensity of ISO/IEC 21031:2024:
//
//	SCI = (O + M) / R
//	O   = E x I, the operational emissions: the energy with the PUE, each
//	      hour at the intensity of that hour
//	M   = embodied carbon x (time / lifetime) x share of the hardware
//	R   = the functional unit
//
// The share of the server is the share of the processor and memory energy
// of the node in the period, and the share of a GPU that of the GPU
// energy.

// Functional units of SCI.
const (
	UnitGeneratedTokens = "1k_generated_tokens"
	UnitRequest         = "request"
	UnitConsumer        = "consumer"
)

// FunctionalUnits lists the accepted units.
var FunctionalUnits = []string{UnitGeneratedTokens, UnitRequest, UnitConsumer}

// Year is the length of a year of hardware lifetime: 365.25 days.
const Year = 8766 * time.Hour

// Embodied is the carbon of making the hardware, in kgCO2e, spread over its
// lifetime. Missing values are nil.
type Embodied struct {
	ServerKg, GPUKg *float64
	LifetimeYears   *float64
}

// Configured reports whether some embodied carbon can be computed.
func (e Embodied) Configured() bool {
	return e.LifetimeYears != nil && *e.LifetimeYears > 0 && (e.ServerKg != nil || e.GPUKg != nil)
}

// Missing names the values that M lacks, as configuration keys.
func (e Embodied) Missing() []string {
	var out []string
	if e.ServerKg == nil {
		out = append(out, "indicators.sci.embodied.server_kgco2e")
	}
	if e.GPUKg == nil {
		out = append(out, "indicators.sci.embodied.gpu_kgco2e")
	}
	if e.LifetimeYears == nil {
		out = append(out, "indicators.sci.embodied.lifetime_years")
	}
	return out
}

// Grams returns M in grams for a period and the shares of the server and
// of the GPU, between 0 and 1. It is nil when the embodied carbon is not
// configured; a part that is not configured adds nothing.
func (e Embodied) Grams(period time.Duration, serverShare, gpuShare float64) *float64 {
	if !e.Configured() || period <= 0 {
		return nil
	}
	used := period.Hours() / (Year.Hours() * *e.LifetimeYears)
	g := 0.0
	if e.ServerKg != nil {
		g += *e.ServerKg * 1000 * used * clamp(serverShare)
	}
	if e.GPUKg != nil {
		g += *e.GPUKg * 1000 * used * clamp(gpuShare)
	}
	return &g
}

func clamp(v float64) float64 {
	switch {
	case v < 0 || v != v:
		return 0
	case v > 1:
		return 1
	}
	return v
}

// Share is the part of whole that part is, zero when whole is not positive.
func Share(part, whole float64) float64 {
	if whole <= 0 {
		return 0
	}
	return clamp(part / whole)
}

// SCI is the carbon intensity of one scope in one period.
type SCI struct {
	Unit string `json:"functional_unit"`
	// Units is R: thousands of generated tokens, requests or consumers.
	Units        float64 `json:"units"`
	OperationalG float64 `json:"operational_g"`
	// EmbodiedG is M; absent when the embodied carbon is not configured.
	EmbodiedG *float64 `json:"embodied_g,omitempty"`
	// GPerUnit is (O + M) / R, or O / R without M; absent when R is zero.
	GPerUnit *float64 `json:"g_per_unit,omitempty"`
}

// ComputeSCI divides the emissions of a scope by its functional units.
func ComputeSCI(unit string, units, operationalG float64, embodiedG *float64) SCI {
	s := SCI{Unit: unit, Units: units, OperationalG: operationalG, EmbodiedG: embodiedG}
	if units > 0 {
		g := operationalG
		if embodiedG != nil {
			g += *embodiedG
		}
		g /= units
		s.GPerUnit = &g
	}
	return s
}

// UnitsOf returns R for a unit from the counts of a scope.
func UnitsOf(unit string, generatedTokens, requests, consumers int64) float64 {
	switch unit {
	case UnitRequest:
		return float64(requests)
	case UnitConsumer:
		return float64(consumers)
	}
	return float64(generatedTokens) / 1000
}
