// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
)

// flagError converts the typed errors of pflag into coded errors so they are
// shown in the user's language.
func flagError(cmd *cobra.Command, err error) error {
	var (
		notExist      *pflag.NotExistError
		valueRequired *pflag.ValueRequiredError
		invalidValue  *pflag.InvalidValueError
		invalidSyntax *pflag.InvalidSyntaxError
	)
	path := cmd.CommandPath()
	switch {
	case errors.As(err, &notExist):
		return help.New(help.GL0003,
			"flag", flagName(notExist.GetSpecifiedName(), notExist.GetSpecifiedShortnames()),
			"command", path,
		).Wrap(err)
	case errors.As(err, &valueRequired):
		return help.New(help.GL0004,
			"flag", flagName(valueRequired.GetSpecifiedName(), valueRequired.GetSpecifiedShortnames()),
			"command", path,
		).Wrap(err)
	case errors.As(err, &invalidValue):
		return help.New(help.GL0005,
			"flag", "--"+invalidValue.GetFlag().Name,
			"value", invalidValue.GetValue(),
			"command", path,
		).Wrap(err)
	case errors.As(err, &invalidSyntax):
		return help.New(help.GL0006,
			"arg", invalidSyntax.GetSpecifiedFlag(),
			"command", path,
		).Wrap(err)
	}
	return help.As(err)
}

// flagName rebuilds the flag as the user typed it: -x when it came from a
// group of shorthands, --name otherwise.
func flagName(name, shorthands string) string {
	if shorthands != "" {
		return "-" + name
	}
	return "--" + name
}

func unknownCommand(parent *cobra.Command, name string) error {
	e := help.New(help.GL0002, "command", name, "parent", parent.CommandPath())
	if s := parent.SuggestionsFor(name); len(s) > 0 {
		e.Args = append(e.Args, "suggestion", s[0])
		e.FixKey = "fix_suggest"
	}
	return e
}

// exactArgs accepts exactly n positional arguments.
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == n {
			return nil
		}
		return help.New(help.GL0015, "command", cmd.CommandPath(), "want", n, "got", len(args))
	}
}

// noArgs rejects positional arguments for commands that take none.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return help.New(help.GL0007, "command", cmd.CommandPath(), "args", strings.Join(args, " "))
}
