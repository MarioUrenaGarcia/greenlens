// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Command greenlens measures and reports the energy use of local AI inference.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/MarioUrenaGarcia/greenlens/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv)
	stop()
	os.Exit(code)
}
