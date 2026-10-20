// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/i18n"
	"github.com/MarioUrenaGarcia/greenlens/internal/version"
)

func newVersionCmd(l *i18n.Localizer) *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   l.T("cli.version.short"),
		Long:    wrap(l.T("cli.version.long"), helpWidth),
		Example: "  greenlens version\n  greenlens version --lang es",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return writeVersion(cmd.OutOrStdout(), l, version.Get())
		},
	}
}

func writeVersion(w io.Writer, l *i18n.Localizer, info version.Info) error {
	unknown := l.T("cli.version.unknown")
	revision := orDefault(info.Revision, unknown)
	if info.Modified {
		revision += " (" + l.T("cli.version.modified") + ")"
	}
	if _, err := fmt.Fprintf(w, "greenlens %s\n", info.Version); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	rows := [][2]string{
		{l.T("cli.version.revision"), revision},
		{l.T("cli.version.commit_time"), orDefault(info.CommitTime, unknown)},
		{l.T("cli.version.go"), info.GoVersion},
		{l.T("cli.version.platform"), info.Platform},
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(tw, "  %s\t%s\n", r[0], r[1]); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
