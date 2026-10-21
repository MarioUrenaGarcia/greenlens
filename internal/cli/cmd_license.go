// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens"
	"github.com/MarioUrenaGarcia/greenlens/internal/output"
	"github.com/MarioUrenaGarcia/greenlens/internal/version"
)

// repository publishes the source code of every version.
const repository = "https://github.com/MarioUrenaGarcia/greenlens"

// commercialContact is where organizations ask for the commercial license.
const commercialContact = "https://github.com/MarioUrenaGarcia"

const copyrightNotice = "Copyright (C) 2026 Mario Ureña García"

// Component is a third-party component included in GreenLens.
type Component struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// LicenseReport is the document of greenlens license.
type LicenseReport struct {
	SchemaVersion int         `json:"schema_version"`
	License       string      `json:"license"`
	Copyright     string      `json:"copyright"`
	Version       string      `json:"version"`
	SourceURL     string      `json:"source_url"`
	Modified      bool        `json:"modified"`
	ThirdParty    []Component `json:"third_party"`
}

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
			return a.writeLicense(cmd, licenseReport(version.Get(), greenlens.ThirdPartyNotices))
		},
	}
	cmd.Flags().BoolVar(&full, "full", false, l.T("cli.license.flag.full"))
	cmd.Flags().BoolVar(&thirdParty, "third-party", false, l.T("cli.license.flag.third_party"))
	return cmd
}

func licenseReport(info version.Info, notices string) LicenseReport {
	return LicenseReport{
		SchemaVersion: 1, License: "AGPL-3.0-only", Copyright: copyrightNotice, Version: info.Version,
		SourceURL: sourceURL(info), Modified: info.Modified || strings.HasSuffix(info.Version, "+dirty"),
		ThirdParty: components(notices),
	}
}

var (
	pseudoVersion = regexp.MustCompile(`\d{14}-([0-9a-f]{12})$`)
	releaseTag    = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$`)
)

// sourceURL points at the source code of the running version, which
// section 13 of the AGPL asks a network service to offer: the release tag,
// else the commit the binary was built from, else the repository.
func sourceURL(info version.Info) string {
	v := strings.TrimSuffix(info.Version, "+dirty")
	switch {
	case releaseTag.MatchString(v) && !pseudoVersion.MatchString(v):
		return repository + "/tree/" + v
	case info.Revision != "":
		return repository + "/tree/" + info.Revision
	case pseudoVersion.MatchString(v):
		return repository + "/tree/" + pseudoVersion.FindStringSubmatch(v)[1]
	}
	return repository
}

// components reads the headings of the third-party notices, each a module
// path or package name followed by its version.
func components(notices string) []Component {
	out := []Component{}
	for _, line := range strings.Split(notices, "\n") {
		h, ok := strings.CutPrefix(line, "## ")
		if !ok {
			continue
		}
		name, ver := h, ""
		if i := strings.LastIndexByte(h, ' '); i > 0 {
			name, ver = h[:i], h[i+1:]
		}
		out = append(out, Component{Name: name, Version: ver})
	}
	return out
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
	b.WriteString("\n" + wrap(l.T("cli.license.commercial", "contact", commercialContact), helpWidth) + "\n\n")
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
