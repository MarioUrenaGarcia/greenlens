// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package bundle compiles the graphical interface with the esbuild Go API, so
// building GreenLens never requires Node.js.
package bundle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

const jsBanner = `/* SPDX-License-Identifier: AGPL-3.0-only AND MIT
 * Copyright (C) 2026 Mario Ureña García
 * Includes Preact and uPlot (MIT). See THIRD_PARTY_NOTICES.md. */`

const cssBanner = `/* SPDX-License-Identifier: AGPL-3.0-only AND MIT
 * Copyright (C) 2026 Mario Ureña García
 * Includes the styles of uPlot (MIT) and loads IBM Plex (SIL OFL 1.1).
 * See THIRD_PARTY_NOTICES.md. */`

// Files lists the output files of Build, relative to the output directory.
// Fonts keep their names, without a content hash, so the list is fixed and
// the build stays byte-identical.
var Files = []string{
	"app.css", "assets/IBMPlexMono-Regular-Latin1.woff2", "assets/IBMPlexSans-Regular-Latin1.woff2",
	"assets/IBMPlexSans-SemiBold-Latin1.woff2", "index.html", "main.js",
}

// Build compiles the interface whose sources live under webDir into outDir.
// The output depends only on the sources: building the same tree twice, from
// any location, produces byte-identical files.
func Build(webDir, outDir string) error {
	src, err := filepath.Abs(webDir)
	if err != nil {
		return err
	}
	out, err := filepath.Abs(outDir)
	if err != nil {
		return err
	}
	res := api.Build(api.BuildOptions{
		AbsWorkingDir:     src,
		EntryPoints:       []string{"src/main.tsx", "styles/app.css"},
		EntryNames:        "[name]",
		Outdir:            out,
		Write:             true,
		Bundle:            true,
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		Format:            api.FormatESModule,
		Target:            api.ES2022,
		Platform:          api.PlatformBrowser,
		JSX:               api.JSXAutomatic,
		Loader:            map[string]api.Loader{".woff2": api.LoaderFile},
		AssetNames:        "assets/[name]",
		JSXImportSource:   "preact",
		NodePaths:         []string{filepath.Join(src, "third_party")},
		Charset:           api.CharsetUTF8,
		LegalComments:     api.LegalCommentsNone,
		Banner:            map[string]string{"js": jsBanner, "css": cssBanner},
		LogLevel:          api.LogLevelSilent,
	})
	if len(res.Errors) > 0 {
		msgs := api.FormatMessages(res.Errors, api.FormatMessagesOptions{Kind: api.ErrorMessage})
		return errors.New("esbuild: " + strings.Join(msgs, ""))
	}
	html, err := os.ReadFile(filepath.Join(src, "src", "index.html")) //nolint:gosec // source tree chosen by the build tool
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "index.html"), html, 0o644); err != nil { //nolint:gosec // public web asset
		return fmt.Errorf("write index.html: %w", err)
	}
	return nil
}
