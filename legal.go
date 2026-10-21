// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package greenlens embeds files kept at the root of the repository, such as
// the license texts and the description of the HTTP API, so that every
// binary carries them.
package greenlens

import (
	"embed"
	"io/fs"
)

// License is the text of the GNU Affero General Public License version 3.
//
//go:embed LICENSE
var License string

// ThirdPartyNotices lists the third-party components with their licenses.
//
//go:embed THIRD_PARTY_NOTICES.md
var ThirdPartyNotices string

// The all: prefix keeps the _index.md pages of the sections, which embed
// leaves out otherwise.
//
//go:embed all:docs
var docs embed.FS

// Docs is the documentation, English at its root and Spanish under es/, as
// the site serves it.
func Docs() fs.FS {
	sub, err := fs.Sub(docs, "docs")
	if err != nil {
		// fs.Sub fails only for an invalid directory name, and "docs" is valid.
		panic(err)
	}
	return sub
}
