// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens"
	"github.com/MarioUrenaGarcia/greenlens/internal/about"
	"github.com/MarioUrenaGarcia/greenlens/internal/output"
)

// LicenseReport is the document of greenlens license.
type LicenseReport = about.Report

func newLicenseCmd(a *app) *cobra.Command {
	l := a.l
	var full, thirdParty bool
	cmd := &cobra.Command{
		Use:     "license",
		Short:   l.T("cli.license.short"),
		Long:    wrap(l.T("cli.license.long"), helpWidth),
		Example: "  greenlens license\n  greenlens license --full | less\n  greenlens license --third-party > notices.md",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			if full || thirdParty {
				if full {
					if _, err := io.WriteString(w, greenlens.License); err != nil {
						return err
					}
				}
				if thirdParty {
					_, err := io.WriteString(w, greenlens.ThirdPartyNotices)
					return err
				}
				return nil
			}
			return a.writeLicense(cmd, about.Get())
		},
	}
	cmd.Flags().BoolVar(&full, "full", false, l.T("cli.license.flag.full"))
	cmd.Flags().BoolVar(&thirdParty, "third-party", false, l.T("cli.license.flag.third_party"))
	return cmd
}

func (a *app) writeLicense(cmd *cobra.Command, rep LicenseReport) error {
	l := a.l
	g := output.Grid{Header: []string{l.T("cli.col.component"), l.T("cli.col.version")}}
	for _, c := range rep.ThirdParty {
		g.Rows = append(g.Rows, []string{c.Name, c.Version})
	}
	if a.out() != output.Table {
		return output.Write(cmd.OutOrStdout(), a.out(), rep, g)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "GreenLens %s\n%s\n\n", rep.Version, rep.Copyright)
	for _, p := range []string{l.T("cli.license.notice"), l.T("cli.license.network")} {
		b.WriteString(wrap(p, helpWidth) + "\n\n")
	}
	b.WriteString(l.T("cli.license.source", "url", rep.SourceURL) + "\n")
	if rep.Modified {
		b.WriteString(wrap(l.T("cli.license.modified"), helpWidth) + "\n")
	}
	b.WriteString("\n" + wrap(l.T("cli.license.commercial", "contact", rep.Commercial), helpWidth) + "\n\n")
	b.WriteString(l.T("cli.license.third_party", "count", len(rep.ThirdParty)) + "\n")
	w := cmd.OutOrStdout()
	if _, err := io.WriteString(w, b.String()); err != nil {
		return err
	}
	if err := output.WriteGrid(w, g); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w, "\n"+l.T("cli.license.more"))
	return err
}
