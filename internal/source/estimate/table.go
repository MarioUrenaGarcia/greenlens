// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package estimate

import (
	"strconv"
	"strings"
)

// Coefficients are the power of one hardware thread at 0% and 100%
// utilization, in watts.
type Coefficients struct {
	Name string
	MinW float64
	MaxW float64
}

// TableSource identifies where the coefficients come from. They are the
// per-vCPU values of Cloud Carbon Footprint, derived from SPECpower_ssj2008
// results, Apache-2.0.
const TableSource = "Cloud Carbon Footprint, packages/aws/src/domain/AwsFootprintEstimationConstants.ts, commit df2098f (2026-03-20)"

// Generic is used when the processor is not in the table: the average of the
// same source.
var Generic = Coefficients{Name: "generic", MinW: 0.74, MaxW: 3.5}

var table = map[string]Coefficients{
	"sandy-bridge":    {"Sandy Bridge", 2.21, 8.63},
	"ivy-bridge":      {"Ivy Bridge", 1.71, 5.56},
	"haswell":         {"Haswell", 1.86, 5.6},
	"broadwell":       {"Broadwell", 0.71, 3.69},
	"skylake":         {"Skylake", 0.61, 4.1},
	"cascade-lake":    {"Cascade Lake", 0.69, 4.06},
	"coffee-lake":     {"Coffee Lake", 1.14, 5.42},
	"ice-lake":        {"Ice Lake", 0.77, 3.76},
	"sapphire-rapids": {"Sapphire Rapids", 1.04, 4.16},
	"emerald-rapids":  {"Emerald Rapids", 0.81, 4.48},
	"granite-rapids":  {"Granite Rapids", 0.58, 2.53},
	"epyc-1":          {"AMD EPYC 1st generation", 0.85, 2.6},
	"epyc-2":          {"AMD EPYC 2nd generation", 0.47, 1.69},
	"epyc-3":          {"AMD EPYC 3rd generation", 0.46, 1.96},
	"epyc-4":          {"AMD EPYC 4th generation", 0.74, 2.28},
	"epyc-5":          {"AMD EPYC 5th generation", 3.68, 8.96},
}

// CPU identifies a processor as /proc/cpuinfo reports it.
type CPU struct {
	Vendor    string
	Family    int
	Model     int
	Stepping  int
	ModelName string
}

// Lookup returns the coefficients of a processor and whether it is in the
// table.
func Lookup(c CPU) (Coefficients, bool) {
	if k := microarchitecture(c); k != "" {
		return table[k], true
	}
	return Generic, false
}

// microarchitecture follows the family and model numbers of
// arch/x86/include/asm/intel-family.h for Intel server parts, and the Zen
// generation ranges of arch/x86/kernel/cpu/amd.c for AMD. AMD coefficients
// describe EPYC servers, so they apply only when the model name says EPYC.
func microarchitecture(c CPU) string {
	switch c.Vendor {
	case "GenuineIntel":
		if c.Family != 6 {
			return ""
		}
		switch c.Model {
		case 0x2D:
			return "sandy-bridge"
		case 0x3E:
			return "ivy-bridge"
		case 0x3F:
			return "haswell"
		case 0x4F, 0x56:
			return "broadwell"
		case 0x55:
			// Skylake-SP and Cascade Lake-SP share the model number and
			// differ in the stepping (Cascade Lake is 6 and 7).
			switch {
			case c.Stepping <= 4:
				return "skylake"
			case c.Stepping == 6 || c.Stepping == 7:
				return "cascade-lake"
			}
		case 0x9E:
			if c.Stepping >= 10 && c.Stepping <= 13 {
				return "coffee-lake"
			}
		case 0x6A, 0x6C:
			return "ice-lake"
		case 0x8F:
			return "sapphire-rapids"
		case 0xCF:
			return "emerald-rapids"
		case 0xAD, 0xAE:
			return "granite-rapids"
		}
	case "AuthenticAMD":
		if !strings.Contains(c.ModelName, "EPYC") {
			return ""
		}
		if z := zen(c.Family, c.Model); z >= 1 && z <= 5 {
			return "epyc-" + strconv.Itoa(z)
		}
	}
	return ""
}

func zen(family, model int) int {
	in := func(lo, hi int) bool { return model >= lo && model <= hi }
	switch family {
	case 0x17:
		switch {
		case in(0x00, 0x2f), in(0x50, 0x5f):
			return 1
		case in(0x30, 0x4f), in(0x60, 0x7f), in(0x90, 0x91), in(0xa0, 0xaf):
			return 2
		}
	case 0x19:
		switch {
		case in(0x00, 0x0f), in(0x20, 0x5f):
			return 3
		case in(0x10, 0x1f), in(0x60, 0xaf):
			return 4
		}
	case 0x1a:
		switch {
		case in(0x00, 0x2f), in(0x40, 0x4f), in(0x60, 0x7f), in(0xd0, 0xd7):
			return 5
		case in(0x50, 0x5f), in(0x80, 0xaf), in(0xc0, 0xcf), in(0xd8, 0xef):
			return 6
		}
	}
	return 0
}
