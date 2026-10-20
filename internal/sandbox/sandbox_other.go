// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

//go:build !linux

package sandbox

// Restrict does nothing outside Linux, where Landlock does not exist.
func Restrict(Rules) (Status, error) { return Status{}, nil }
