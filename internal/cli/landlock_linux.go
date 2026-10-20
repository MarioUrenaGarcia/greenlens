// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

//go:build linux

package cli

import llsyscall "github.com/landlock-lsm/go-landlock/landlock/syscall"

func landlockABI() int {
	v, err := llsyscall.LandlockGetABIVersion()
	if err != nil {
		return 0
	}
	return v
}
