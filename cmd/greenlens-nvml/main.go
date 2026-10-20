// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Command greenlens-nvml reads NVIDIA GPUs for GreenLens. It loads the
// NVIDIA Management Library, which needs the dynamic loader, so that the
// greenlens binary can stay static. The agent starts it and talks to it
// over standard input and output; it is not meant to be run by hand.
package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/MarioUrenaGarcia/greenlens/internal/nvmlhelper"
)

func main() {
	if len(os.Args) > 1 {
		os.Exit(2)
	}
	// Stopping a service or pressing Ctrl+C signals the whole process
	// group. The helper exits when the agent closes its input, after
	// shutting NVML down, and is killed if the agent dies first.
	signal.Ignore(syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	if err := nvmlhelper.Serve(os.Stdin, os.Stdout, nvmlhelper.Open); err != nil {
		os.Exit(1)
	}
}
