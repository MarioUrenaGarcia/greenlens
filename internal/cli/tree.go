// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/i18n"
)

// Tree returns the command tree with its texts in lang, for the generated
// command reference.
func Tree(lang string) (*cobra.Command, error) {
	l, err := i18n.New(lang)
	if err != nil {
		return nil, err
	}
	a := &app{l: l, stdout: io.Discard, stderr: io.Discard, getenv: func(string) string { return "" }}
	return newRoot(a), nil
}

// FlagsPlaceholder returns the translated placeholder of the usage line.
func FlagsPlaceholder(l *i18n.Localizer) string { return l.T("cli.usage.flags_placeholder") }
