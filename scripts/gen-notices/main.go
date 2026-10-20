// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Command gen-notices writes THIRD_PARTY_NOTICES.md to standard output: the
// license texts of every Go module compiled into GreenLens and of every
// vendored web package. Run it from the repository root with "make notices".
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const mainModule = "github.com/MarioUrenaGarcia/greenlens"

var licenseName = regexp.MustCompile(`(?i)^(licen[cs]e|copying)(\.(md|txt))?$`)

type goPackage struct {
	ImportPath string
	Dir        string
	Standard   bool
	Module     *struct {
		Path    string
		Version string
		Dir     string
	}
}

type component struct {
	name    string
	version string
	license string
	files   []licenseFile
}

type licenseFile struct {
	path string
	text string
}

func main() {
	comps, err := goComponents()
	if err == nil {
		var web []component
		web, err = webComponents("web/third_party/manifest.json")
		comps = append(comps, web...)
	}
	if err == nil {
		err = write(os.Stdout, comps)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen-notices:", err)
		os.Exit(1)
	}
}

// goComponents lists the modules of the non-test packages of the main module
// and collects every license file between each package directory and its
// module root, so licenses of vendored subdirectories are not missed.
func goComponents() ([]component, error) {
	out, err := exec.CommandContext(context.Background(), "go", "list", "-deps", "-json", "./...").Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	byModule := map[string]*component{}
	seenFile := map[string]bool{}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p goPackage
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}
		if p.Standard || p.Module == nil || p.Module.Path == mainModule {
			continue
		}
		c := byModule[p.Module.Path]
		if c == nil {
			c = &component{name: p.Module.Path, version: p.Module.Version}
			byModule[p.Module.Path] = c
		}
		for dir := p.Dir; ; dir = filepath.Dir(dir) {
			files, err := licenseFiles(dir, p.Module.Dir)
			if err != nil {
				return nil, err
			}
			for _, f := range files {
				key := p.Module.Path + "/" + f.path
				if !seenFile[key] {
					seenFile[key] = true
					c.files = append(c.files, f)
				}
			}
			if dir == p.Module.Dir || len(dir) <= len(p.Module.Dir) {
				break
			}
		}
	}
	var comps []component
	for _, c := range byModule {
		if len(c.files) == 0 {
			return nil, fmt.Errorf("%s %s has no license file", c.name, c.version)
		}
		slices.SortFunc(c.files, func(a, b licenseFile) int { return strings.Compare(a.path, b.path) })
		comps = append(comps, *c)
	}
	slices.SortFunc(comps, func(a, b component) int { return strings.Compare(a.name, b.name) })
	return comps, nil
}

func licenseFiles(dir, moduleDir string) ([]licenseFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []licenseFile
	for _, e := range entries {
		if e.IsDir() || !licenseName.MatchString(e.Name()) {
			continue
		}
		full := filepath.Join(dir, e.Name())
		text, err := os.ReadFile(full) //nolint:gosec // paths come from the Go module cache
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(moduleDir, full)
		if err != nil {
			return nil, err
		}
		out = append(out, licenseFile{path: filepath.ToSlash(rel), text: string(text)})
	}
	return out, nil
}

func webComponents(manifestPath string) ([]component, error) {
	data, err := os.ReadFile(manifestPath) //nolint:gosec // fixed path inside the repository
	if err != nil {
		return nil, err
	}
	var m struct {
		Packages []struct {
			Name        string `json:"name"`
			Version     string `json:"version"`
			License     string `json:"license"`
			LicenseFile string `json:"license_file"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", manifestPath, err)
	}
	var comps []component
	for _, p := range m.Packages {
		text, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath), p.Name, p.LicenseFile))
		if err != nil {
			return nil, err
		}
		comps = append(comps, component{
			name:    p.Name + " (npm)",
			version: p.Version,
			license: p.License,
			files:   []licenseFile{{path: p.LicenseFile, text: string(text)}},
		})
	}
	return comps, nil
}

func write(w io.Writer, comps []component) error {
	var b strings.Builder
	b.WriteString("# Third-party notices\n\n")
	b.WriteString("GreenLens includes the third-party components listed below. Each one is\n")
	b.WriteString("distributed under the license reproduced in its section. This file is\n")
	b.WriteString("generated with `make notices`.\n")
	for _, c := range comps {
		b.WriteString("\n## " + c.name + " " + c.version + "\n")
		if c.license != "" {
			b.WriteString("\nLicense: " + c.license + "\n")
		}
		for _, f := range c.files {
			b.WriteString("\nFile: " + f.path + "\n\n````text\n")
			b.WriteString(strings.TrimRight(f.text, "\n") + "\n````\n")
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
