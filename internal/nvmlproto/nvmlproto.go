// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package nvmlproto is the protocol between GreenLens and greenlens-nvml,
// the helper that loads the NVIDIA Management Library. Loading a shared
// library needs the dynamic loader, so it lives in a separate process and
// the main binary stays static. Each request and each response is one JSON
// object on one line.
package nvmlproto

import (
	"fmt"
	"strconv"
	"strings"
)

// Version changes when a message changes incompatibly.
const Version = 1

// Operations.
const (
	OpProbe = "probe"
	OpRead  = "read"
)

// Request asks the helper for one operation.
type Request struct {
	Op string `json:"op"`
}

// Return codes of nvmlReturn_t used by GreenLens, from nvml.h.
const (
	Success           = 0
	ErrNotSupported   = 3
	ErrNoPermission   = 4
	ErrNotFound       = 6
	ErrInsufficient   = 7
	ErrDriverNotLoad  = 9
	ErrLibraryMissing = 12
	ErrGPULost        = 15
)

// Error is an NVML call that failed.
type Error struct {
	Call   string `json:"call"`
	Return int    `json:"return"`
	Text   string `json:"text,omitempty"`
}

func (e *Error) Error() string {
	if e.Text != "" {
		return fmt.Sprintf("%s: %s (%d)", e.Call, e.Text, e.Return)
	}
	return fmt.Sprintf("%s returned %d", e.Call, e.Return)
}

// Device is a GPU found by NVML.
type Device struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	// PCI is the address in the form used by sysfs, "0000:01:00.0".
	PCI string `json:"pci"`
	// Energy is true when the device has a total energy counter, on Volta
	// and later.
	Energy bool `json:"energy"`
	// Power is true when the device reports its power draw.
	Power bool `json:"power"`
}

// ProbeResponse answers OpProbe.
type ProbeResponse struct {
	Version int      `json:"version"`
	Driver  string   `json:"driver,omitempty"`
	Devices []Device `json:"devices,omitempty"`
	Error   *Error   `json:"error,omitempty"`
}

// Process is the use a process made of a GPU since the previous read.
type Process struct {
	PID int `json:"pid"`
	// SM is the percentage of time its kernels ran on the streaming
	// multiprocessors.
	SM uint32 `json:"sm"`
}

// Sample is one reading of a device.
type Sample struct {
	Index int `json:"index"`
	// EnergyMJ is the energy since the driver was loaded, in millijoules,
	// for devices with an energy counter.
	EnergyMJ uint64 `json:"energy_mj,omitempty"`
	// PowerMW is the power draw in milliwatts, for the other devices.
	PowerMW uint32 `json:"power_mw,omitempty"`
	// Util is the percentage of time a kernel ran on the GPU.
	Util uint32 `json:"util"`
	// PIDs are the processes that hold a context on the device.
	PIDs []int `json:"pids,omitempty"`
	// Processes report their use since the previous read.
	Processes []Process `json:"processes,omitempty"`
	Error     *Error    `json:"error,omitempty"`
}

// ReadResponse answers OpRead.
type ReadResponse struct {
	Samples []Sample `json:"samples,omitempty"`
	Error   *Error   `json:"error,omitempty"`
}

// PCIAddress converts the bus id NVML reports, "00000000:01:00.0", to the
// form used by sysfs, "0000:01:00.0".
func PCIAddress(busID string) (string, error) {
	domain, rest, ok := strings.Cut(strings.TrimRight(busID, "\x00"), ":")
	if !ok {
		return "", fmt.Errorf("bus id %q has no domain", busID)
	}
	bus, rest, ok := strings.Cut(rest, ":")
	if !ok {
		return "", fmt.Errorf("bus id %q has no bus", busID)
	}
	dev, fn, ok := strings.Cut(rest, ".")
	if !ok {
		return "", fmt.Errorf("bus id %q has no function", busID)
	}
	var v [4]uint64
	for i, p := range []struct {
		s    string
		bits int
	}{{domain, 32}, {bus, 8}, {dev, 8}, {fn, 8}} {
		n, err := strconv.ParseUint(p.s, 16, p.bits)
		if err != nil {
			return "", fmt.Errorf("bus id %q: %w", busID, err)
		}
		v[i] = n
	}
	if v[2] > 31 || v[3] > 7 {
		return "", fmt.Errorf("bus id %q is out of range", busID)
	}
	return fmt.Sprintf("%04x:%02x:%02x.%x", v[0], v[1], v[2], v[3]), nil
}
