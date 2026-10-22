// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"syscall"

	"github.com/landlock-lsm/go-landlock/landlock"
	llsyscall "github.com/landlock-lsm/go-landlock/landlock/syscall"
)

// Restrict confines the calling process, all of its threads and every child
// it starts to the given rules. The restriction cannot be lifted.
//
// The newest Landlock ABI known to the library is requested and downgraded to
// what the kernel supports. On a kernel without Landlock nothing changes and
// Status.Enforced is false, so the caller can report that confinement is not
// available; any other failure is returned as an error.
func Restrict(r Rules) (Status, error) {
	abi, err := llsyscall.LandlockGetABIVersion()
	if err != nil {
		if errors.Is(err, syscall.ENOSYS) || errors.Is(err, syscall.EOPNOTSUPP) {
			return Status{}, nil
		}
		return Status{}, fmt.Errorf("query landlock ABI: %w", err)
	}
	err = landlock.V10.BestEffort().RestrictPaths(
		landlock.RODirs(r.ReadOnly...),
		landlock.RWDirs(r.ReadWrite...),
		landlock.ROFiles(r.Sockets...).WithResolveUnix().IgnoreIfMissing(),
	)
	if err != nil {
		return Status{ABI: abi}, fmt.Errorf("apply landlock rules: %w", err)
	}
	return Status{ABI: abi, Enforced: true}, nil
}
