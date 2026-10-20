// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package sandbox confines the agent's own file access with Landlock, so a
// compromised agent cannot read or write outside the paths it needs.
package sandbox

// Rules lists the directory trees that stay accessible after Restrict.
// Everything else on the file system becomes inaccessible.
type Rules struct {
	// ReadOnly trees can be read and their files executed.
	ReadOnly []string
	// ReadWrite trees can be read, written and modified.
	ReadWrite []string
}

// Status reports what Restrict applied.
type Status struct {
	// ABI is the Landlock ABI version of the running kernel; 0 means the
	// kernel does not provide Landlock or has it disabled.
	ABI int
	// Enforced is true when the restriction is in effect.
	Enforced bool
}
