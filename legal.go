// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package greenlens holds the license texts at the root of the repository,
// embedded so that every binary can show them.
package greenlens

import _ "embed" // for go:embed

// License is the text of the GNU Affero General Public License version 3.
//
//go:embed LICENSE
var License string

// ThirdPartyNotices lists the third-party components with their licenses.
//
//go:embed THIRD_PARTY_NOTICES.md
var ThirdPartyNotices string
