// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestAssetsAreEmbedded(t *testing.T) {
	assets := Assets()
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{`src="main.js"`, `href="app.css"`} {
		if !strings.Contains(string(index), ref) {
			t.Errorf("index.html does not reference %s", ref)
		}
	}
	for _, name := range []string{"main.js", "app.css"} {
		data, err := fs.ReadFile(assets, name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data[:min(len(data), 200)]), "SPDX-License-Identifier") {
			t.Errorf("%s lacks its license banner", name)
		}
	}
}

type manifest struct {
	Packages []struct {
		Name        string            `json:"name"`
		Version     string            `json:"version"`
		License     string            `json:"license"`
		LicenseFile string            `json:"license_file"`
		Source      string            `json:"source"`
		Integrity   string            `json:"integrity"`
		Files       map[string]string `json:"files"`
	} `json:"packages"`
}

var allowedLicenses = []string{"MIT", "BSD-2-Clause", "BSD-3-Clause", "Apache-2.0", "ISC", "OFL-1.1"}

// TestThirdPartyFilesMatchTheManifest checks every vendored file against the
// SHA-256 recorded when it was copied, and rejects files the manifest does
// not list.
func TestThirdPartyFilesMatchTheManifest(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("third_party", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{"manifest.json": true}
	for _, p := range m.Packages {
		if !slices.Contains(allowedLicenses, p.License) {
			t.Errorf("%s: license %q is not allowed", p.Name, p.License)
		}
		if _, ok := p.Files[p.LicenseFile]; !ok {
			t.Errorf("%s: license file %q is not vendored", p.Name, p.LicenseFile)
		}
		if !strings.HasPrefix(p.Integrity, "sha512-") || !strings.HasPrefix(p.Source, "https://registry.npmjs.org/") {
			t.Errorf("%s: source or integrity missing", p.Name)
		}
		for name, want := range p.Files {
			path := filepath.Join(p.Name, filepath.FromSlash(name))
			listed[filepath.ToSlash(path)] = true
			b, err := os.ReadFile(filepath.Join("third_party", path))
			if err != nil {
				t.Error(err)
				continue
			}
			sum := sha256.Sum256(b)
			if got := hex.EncodeToString(sum[:]); got != want {
				t.Errorf("%s: sha256 %s, manifest says %s", path, got, want)
			}
		}
	}
	err = fs.WalkDir(os.DirFS("third_party"), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && !listed[p] {
			t.Errorf("third_party/%s is not listed in the manifest", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
