// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

//go:build !linux

package perfevent

import "errors"

// SystemOpener reports that perf_event exists only on Linux.
type SystemOpener struct{}

// Open implements Opener.
func (SystemOpener) Open(uint32, uint64, int) (Counter, error) {
	return nil, errors.ErrUnsupported
}

func isPermission(error) bool { return false }
