// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

//go:build linux && (amd64 || arm64)

package nvmlhelper

import (
	"testing"
	"unsafe"
)

// The sizes and offsets of the structures NVML fills must match nvml.h on
// 64-bit Linux, or the library would write past them.
func TestStructLayout(t *testing.T) {
	var p pciInfo
	var pi processInfo
	var ps processSample
	for _, c := range []struct {
		name      string
		got, want uintptr
	}{
		{"sizeof(nvmlPciInfo_t)", unsafe.Sizeof(p), 68},
		{"offsetof(nvmlPciInfo_t, busId)", unsafe.Offsetof(p.busID), 36},
		{"sizeof(nvmlUtilization_t)", unsafe.Sizeof(utilization{}), 8},
		{"sizeof(nvmlProcessInfo_v2_t)", unsafe.Sizeof(pi), 24},
		{"sizeof(nvmlProcessUtilizationSample_t)", unsafe.Sizeof(ps), 32},
		{"offsetof(nvmlProcessUtilizationSample_t, timeStamp)", unsafe.Offsetof(ps.timeStamp), 8},
		{"offsetof(nvmlProcessUtilizationSample_t, smUtil)", unsafe.Offsetof(ps.smUtil), 16},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}
