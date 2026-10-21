// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package bundle

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// webDir is the interface source tree, relative to this package.
const webDir = "../.."

// TestDistIsUpToDateAndReproducible builds the interface from a copy of the
// sources in another location and compares the result with web/dist. A
// difference means either that web/dist is stale (run "make web") or that the
// build depends on where it runs.
func TestDistIsUpToDateAndReproducible(t *testing.T) {
	copyDir := filepath.Join(t.TempDir(), "web")
	for _, d := range []string{"assets", "src", "styles", "third_party"} {
		if err := os.CopyFS(filepath.Join(copyDir, d), os.DirFS(filepath.Join(webDir, d))); err != nil {
			t.Fatal(err)
		}
	}
	out := t.TempDir()
	if err := Build(copyDir, out); err != nil {
		t.Fatal(err)
	}
	built := listFiles(t, out)
	if !slices.Equal(built, Files) {
		t.Fatalf("build produced %v, want %v", built, Files)
	}
	committed := listFiles(t, filepath.Join(webDir, "dist"))
	if !slices.Equal(committed, Files) {
		t.Fatalf("web/dist holds %v, want %v; run make web", committed, Files)
	}
	for _, name := range Files {
		a, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(webDir, "dist", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("web/dist/%s differs from a fresh build; run make web", name)
		}
	}
}

func TestBuildReportsErrors(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"src", "styles"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.tsx"), []byte("import x from \"missing\";\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "styles", "app.css"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Build(dir, t.TempDir()); err == nil {
		t.Fatal("unresolvable import did not fail the build")
	}
}

func listFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := fs.WalkDir(os.DirFS(dir), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}
