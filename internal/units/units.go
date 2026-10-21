// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package units formats physical quantities for people: energy in mJ, J, Wh
// or kWh, power in mW, W or kW, and mass of CO2e in g, kg or t, each with
// three significant digits. Raw mode keeps the base unit with full
// precision for scripts.
package units

import (
	"math"
	"strconv"
)

func format(v float64, unit string) string {
	a := math.Abs(v)
	prec := 2
	switch {
	case a >= 100:
		prec = 0
	case a >= 10:
		prec = 1
	}
	return strconv.FormatFloat(v, 'f', prec, 64) + " " + unit
}

func raw(v float64, unit string) string {
	return strconv.FormatFloat(v, 'g', -1, 64) + " " + unit
}

// Energy formats joules.
func Energy(j float64, asRaw bool) string {
	if asRaw {
		return raw(j, "J")
	}
	a := math.Abs(j)
	switch {
	case a == 0:
		return "0 J"
	case a < 1:
		return format(j*1e3, "mJ")
	case a < 3600:
		return format(j, "J")
	case a < 3.6e6:
		return format(j/3600, "Wh")
	default:
		return format(j/3.6e6, "kWh")
	}
}

// Power formats watts.
func Power(w float64, asRaw bool) string {
	if asRaw {
		return raw(w, "W")
	}
	a := math.Abs(w)
	switch {
	case a == 0:
		return "0 W"
	case a < 1:
		return format(w*1e3, "mW")
	case a < 1000:
		return format(w, "W")
	default:
		return format(w/1000, "kW")
	}
}

// CO2e formats grams of CO2 equivalent.
func CO2e(g float64, asRaw bool) string {
	if asRaw {
		return raw(g, "gCO2e")
	}
	a := math.Abs(g)
	switch {
	case a < 1000:
		return format(g, "gCO2e")
	case a < 1e6:
		return format(g/1000, "kgCO2e")
	default:
		return format(g/1e6, "tCO2e")
	}
}

// Money formats an amount in a currency.
func Money(v float64, currency string, asRaw bool) string {
	if asRaw {
		return raw(v, currency)
	}
	return strconv.FormatFloat(v, 'f', 4, 64) + " " + currency
}

// Bytes formats a size in bytes with binary prefixes.
func Bytes(n int64, asRaw bool) string {
	if asRaw || n < 1024 && n > -1024 {
		return strconv.FormatInt(n, 10) + " B"
	}
	v := float64(n) / 1024
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	i := 0
	for math.Abs(v) >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return format(v, units[i])
}
