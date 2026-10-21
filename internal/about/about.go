// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package about describes the running version of GreenLens for its users:
// its license, where its source code is, as section 13 of the AGPL asks a
// network service to offer, and the third-party components it includes.
package about

import (
	"regexp"
	"strings"

	"github.com/MarioUrenaGarcia/greenlens"
	"github.com/MarioUrenaGarcia/greenlens/internal/version"
)

// Repository publishes the source code of every version.
const Repository = "https://github.com/MarioUrenaGarcia/greenlens"

// CommercialContact is where organizations ask for the commercial license.
const CommercialContact = "https://github.com/MarioUrenaGarcia"

// Copyright is the notice of the copyright holder.
const Copyright = "Copyright (C) 2026 Mario Ureña García"

// Component is a third-party component included in GreenLens.
type Component struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Report describes the running version.
type Report struct {
	SchemaVersion int         `json:"schema_version"`
	License       string      `json:"license"`
	Copyright     string      `json:"copyright"`
	Version       string      `json:"version"`
	Revision      string      `json:"revision,omitempty"`
	SourceURL     string      `json:"source_url"`
	Modified      bool        `json:"modified"`
	Commercial    string      `json:"commercial_contact"`
	ThirdParty    []Component `json:"third_party"`
}

// Get describes the running binary.
func Get() Report { return For(version.Get(), greenlens.ThirdPartyNotices) }

// For describes a build from its version information and notices.
func For(info version.Info, notices string) Report {
	return Report{
		SchemaVersion: 1, License: "AGPL-3.0-only", Copyright: Copyright, Version: info.Version, Revision: info.Revision,
		SourceURL: SourceURL(info), Modified: info.Modified || strings.HasSuffix(info.Version, "+dirty"),
		Commercial: CommercialContact, ThirdParty: Components(notices),
	}
}

var (
	pseudoVersion = regexp.MustCompile(`\d{14}-([0-9a-f]{12})$`)
	releaseTag    = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$`)
)

// SourceURL points at the source code of a version: its release tag, else
// the commit it was built from, else the repository.
func SourceURL(info version.Info) string {
	v := strings.TrimSuffix(info.Version, "+dirty")
	switch {
	case releaseTag.MatchString(v) && !pseudoVersion.MatchString(v):
		return Repository + "/tree/" + v
	case info.Revision != "":
		return Repository + "/tree/" + info.Revision
	case pseudoVersion.MatchString(v):
		return Repository + "/tree/" + pseudoVersion.FindStringSubmatch(v)[1]
	}
	return Repository
}

// Components reads the headings of the third-party notices, each a module
// path or package name followed by its version.
func Components(notices string) []Component {
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
