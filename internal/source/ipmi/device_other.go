// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

//go:build !linux

package ipmi

import "errors"

// SystemOpener reports that the IPMI device exists only on Linux.
type SystemOpener struct{}

// Open implements Opener.
func (SystemOpener) Open(string) (Device, error) { return nil, errors.ErrUnsupported }
