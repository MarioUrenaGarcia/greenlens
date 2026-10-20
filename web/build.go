// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

//go:build ignore

// Build regenerates web/dist from the interface sources. Run it from the
// repository root with "make web".
package main

import (
	"fmt"
	"os"

	"github.com/MarioUrenaGarcia/greenlens/web/internal/bundle"
)

func main() {
	// Stale files from earlier builds must not end up embedded in the binary.
	if err := os.RemoveAll("web/dist"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := bundle.Build("web", "web/dist"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
