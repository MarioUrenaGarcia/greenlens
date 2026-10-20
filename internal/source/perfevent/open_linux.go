// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

//go:build linux

package perfevent

import (
	"encoding/binary"
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// SystemOpener opens counters with the perf_event_open system call.
type SystemOpener struct{}

type fdCounter struct{ fd int }

// Open implements Opener. pid -1 with a CPU counts every task on that CPU,
// which is how the kernel exposes the package energy counters.
func (SystemOpener) Open(pmuType uint32, config uint64, cpu int) (Counter, error) {
	attr := unix.PerfEventAttr{Type: pmuType, Config: config}
	attr.Size = uint32(unsafe.Sizeof(attr))
	fd, err := unix.PerfEventOpen(&attr, -1, cpu, -1, unix.PERF_FLAG_FD_CLOEXEC)
	if err != nil {
		return nil, err
	}
	return &fdCounter{fd: fd}, nil
}

func (c *fdCounter) Read() (uint64, error) {
	var buf [8]byte
	n, err := unix.Read(c.fd, buf[:])
	if err != nil {
		return 0, err
	}
	if n != len(buf) {
		return 0, fmt.Errorf("short perf counter read: %d bytes", n)
	}
	return binary.NativeEndian.Uint64(buf[:]), nil
}

func (c *fdCounter) Close() error { return unix.Close(c.fd) }

func isPermission(err error) bool {
	return errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)
}
