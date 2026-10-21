// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package indicators turns energy into the environmental and economic
// indicators GreenLens reports:
//
//	kWh   = joules / 3.6e6
//	gCO2e = kWh x emission factor x PUE
//	cost  = kWh x tariff x PUE
//	water = kWh x WUE
//
// The emission factor can follow an hourly profile of the grid and the
// tariff can have time-of-use periods; the energy of each hour then takes
// the values of that hour, in the local time of the agent.
package indicators

import (
	"errors"
	"fmt"
	"time"
)

// JoulesPerKWh converts energy units.
const JoulesPerKWh = 3.6e6

// DefaultEmissionFactor is the emission factor of the Mexican National
// Electric System (FESEN) for 2024, 0.444 tCO2e/MWh, in gCO2e/kWh.
const DefaultEmissionFactor = 444.0

// DefaultEmissionSource cites DefaultEmissionFactor.
const DefaultEmissionSource = "FESEN 2024, SEMARNAT notice of 2025-02-28"

// Factors are the conversion parameters. Optional factors are nil when not
// configured, and the matching indicator is then not reported.
type Factors struct {
	EmissionGPerKWh float64
	EmissionSource  string
	PUE             float64
	WUELPerKWh      *float64
	TariffPerKWh    *float64
	Currency        string
	// Location is the time zone of the profile and of the periods.
	Location *time.Location
	// Profile, when set, replaces the emission factor hour by hour.
	Profile *Profile
	// Periods are time-of-use prices; TariffPerKWh applies outside them.
	Periods []Period
}

// Default returns the factors used when nothing is configured.
func Default() Factors {
	return Factors{EmissionGPerKWh: DefaultEmissionFactor, EmissionSource: DefaultEmissionSource, PUE: 1, Currency: "MXN"}
}

// Validate checks that the factors are physically meaningful.
func (f Factors) Validate() error {
	var errs []error
	if f.EmissionGPerKWh < 0 {
		errs = append(errs, fmt.Errorf("emission factor %v is negative", f.EmissionGPerKWh))
	}
	if f.PUE < 1 {
		errs = append(errs, fmt.Errorf("PUE %v is below 1", f.PUE))
	}
	if f.WUELPerKWh != nil && *f.WUELPerKWh < 0 {
		errs = append(errs, fmt.Errorf("WUE %v is negative", *f.WUELPerKWh))
	}
	if f.TariffPerKWh != nil && *f.TariffPerKWh < 0 {
		errs = append(errs, fmt.Errorf("tariff %v is negative", *f.TariffPerKWh))
	}
	return errors.Join(errs...)
}

// Values are the indicators of an amount of energy.
type Values struct {
	KWh    float64  `json:"kwh"`
	CO2eG  float64  `json:"co2e_g"`
	Cost   *float64 `json:"cost,omitempty"`
	WaterL *float64 `json:"water_l,omitempty"`
}

// Compute returns the indicators of joules of energy.
func (f Factors) Compute(joules float64) Values {
	kwh := joules / JoulesPerKWh
	v := Values{KWh: kwh, CO2eG: kwh * f.EmissionGPerKWh * f.PUE}
	if f.TariffPerKWh != nil {
		c := kwh * *f.TariffPerKWh * f.PUE
		v.Cost = &c
	}
	if f.WUELPerKWh != nil {
		w := kwh * *f.WUELPerKWh
		v.WaterL = &w
	}
	return v
}
