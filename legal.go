// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package greenlens embeds files kept at the root of the repository, such as
// the license texts and the description of the HTTP API, so that every
// binary carries them.
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
