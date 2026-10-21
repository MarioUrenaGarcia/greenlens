// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package bundle compiles the graphical interface with the esbuild Go API, so
// building GreenLens never requires Node.js.
package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

const swBanner = `/* SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (C) 2026 Mario Ureña García */`

// noTsconfig keeps esbuild from reading web/tsconfig.json, which serves the
// type checker only: every option of the output is set here, so the build
// is the same with or without that file.
const noTsconfig = "{}"

// Files lists the output files of Build, relative to the output directory.
// Fonts keep their names, without a content hash, so the list is fixed and
// the build stays byte-identical.
var Files = []string{
	"app.css", "assets/IBMPlexMono-Regular-Latin1.woff2", "assets/IBMPlexSans-Regular-Latin1.woff2",
	"assets/IBMPlexSans-SemiBold-Latin1.woff2", "assets/icon-192.png", "assets/icon-512.png", "assets/icon.svg",
	"index.html", "main.js", "manifest.webmanifest", "sw.js",
}

// copied are the files taken from the sources as they are, by output name.
var copied = map[string]string{
	"index.html":           "src/index.html",
	"manifest.webmanifest": "src/manifest.webmanifest",
	"assets/icon.svg":      "assets/icons/icon.svg",
	"assets/icon-192.png":  "assets/icons/icon-192.png",
	"assets/icon-512.png":  "assets/icons/icon-512.png",
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
		TsconfigRaw:       noTsconfig,
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
	for name, from := range copied {
		data, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(from))) //nolint:gosec // source tree chosen by the build tool
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(out, filepath.FromSlash(name)), data, 0o644); err != nil { //nolint:gosec // public web asset
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return buildWorker(src, out)
}

// buildWorker compiles the service worker with the list of files it keeps
// for opening without the agent, and a hash of their contents that names
// its cache, so a new build replaces the copies of the old one.
func buildWorker(src, out string) error {
	shell := []string{"/"}
	h := sha256.New()
	for _, name := range Files {
		if name == "sw.js" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(name))) //nolint:gosec // files written by this build
		if err != nil {
			return err
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(data)
		if name != "index.html" {
			shell = append(shell, "/"+name)
		}
	}
	slices.Sort(shell)
	list, err := json.Marshal(shell)
	if err != nil {
		return err
	}
	version, err := json.Marshal(hex.EncodeToString(h.Sum(nil))[:16])
	if err != nil {
		return err
	}
	res := api.Build(api.BuildOptions{
		AbsWorkingDir:     src,
		EntryPoints:       []string{"src/sw.ts"},
		EntryNames:        "[name]",
		Outdir:            out,
		Write:             true,
		Bundle:            true,
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		Format:            api.FormatIIFE,
		Target:            api.ES2022,
		Platform:          api.PlatformBrowser,
		Define:            map[string]string{"__SHELL__": string(list), "__VERSION__": string(version)},
		TsconfigRaw:       noTsconfig,
		Charset:           api.CharsetUTF8,
		LegalComments:     api.LegalCommentsNone,
		Banner:            map[string]string{"js": swBanner},
		LogLevel:          api.LogLevelSilent,
	})
	if len(res.Errors) > 0 {
		msgs := api.FormatMessages(res.Errors, api.FormatMessagesOptions{Kind: api.ErrorMessage})
		return errors.New("esbuild: " + strings.Join(msgs, ""))
	}
	return nil
}
