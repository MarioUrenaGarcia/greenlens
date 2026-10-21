// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package greenlens

import _ "embed" // for go:embed

// OpenAPI describes the HTTP API of the agent, which serves it at
// /api/v1/openapi.yaml.
//
//go:embed api/openapi.yaml
var OpenAPI []byte
