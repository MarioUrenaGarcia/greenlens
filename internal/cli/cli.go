// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package cli builds the greenlens command tree. Commands only parse input
// and format output; the behavior lives in the packages they call.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/i18n"
)

// Execute runs the command line with args and returns the process exit code.
// getenv is os.Getenv in production and a map lookup in tests.
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	lang, langErr := i18n.Resolve(langFlag(args), getenv)
	l, err := i18n.New(lang)
	if err != nil {
		// Only possible if the embedded catalogs are broken, which the test
		// suite rejects; there is no catalog left to translate this message.
		_, _ = fmt.Fprintln(stderr, err)
		return help.ExitError
	}
	if langErr != nil {
		_, _ = fmt.Fprintln(stderr, help.Render(l, languageError(langErr)))
		return help.ExitUsage
	}

	root := newRoot(l)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintln(stderr, help.Render(l, err))
		return help.ExitCode(err)
	}
	return help.ExitOK
}

// langFlag finds the value of --lang before the command tree is built,
// because command descriptions are translated when the tree is created.
func langFlag(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if v, ok := strings.CutPrefix(a, "--lang="); ok {
			return v
		}
		if a == "--lang" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func languageError(err error) error {
	var u *i18n.UnsupportedError
	if !errors.As(err, &u) {
		return err
	}
	return help.New(help.GL0008,
		"lang", u.Value,
		"source", u.Source,
		"supported", strings.Join(i18n.Supported, ", "),
	).Wrap(err)
}
