// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestParseCatalog(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    map[string]string
		wantErr string
	}{
		{
			name: "tables, comments and escapes",
			in:   "# comment\n\ntop = \"a\"\n[cli.root]\nshort = \"say \\\"hi\\\"\\n\"\n  indented = \"b\"\n",
			want: map[string]string{"top": "a", "cli.root.short": "say \"hi\"\n", "cli.root.indented": "b"},
		},
		{name: "duplicate key", in: "a = \"1\"\na = \"2\"\n", wantErr: "duplicate key"},
		{name: "trailing comment", in: "a = \"1\" # no\n", wantErr: "expected a comment"},
		{name: "two strings", in: "a = \"1\" \"2\"\n", wantErr: "invalid string"},
		{name: "unquoted value", in: "a = 1\n", wantErr: "expected a comment"},
		{name: "literal string", in: "a = 'x'\n", wantErr: "expected a comment"},
		{name: "array table", in: "[[a]]\n", wantErr: "expected a comment"},
		{name: "bad escape", in: "a = \"\\q\"\n", wantErr: "invalid string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCatalog([]byte(tt.in))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func FuzzParseCatalog(f *testing.F) {
	f.Add([]byte("[a.b]\nc = \"d {x}\"\n"))
	f.Add([]byte("# c\nk = \"\\u00e9\"\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		msgs, err := ParseCatalog(data)
		if err != nil {
			return
		}
		for k := range msgs {
			if k == "" || strings.HasPrefix(k, ".") || strings.HasSuffix(k, ".") {
				t.Fatalf("invalid key %q accepted", k)
			}
		}
	})
}

func TestCatalogsHaveTheSameKeysAndPlaceholders(t *testing.T) {
	ref, err := Catalog(Default)
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range Supported {
		msgs, err := Catalog(lang)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range ref {
			other, ok := msgs[k]
			if !ok {
				t.Errorf("%s: missing key %s", lang, k)
				continue
			}
			a, b := Placeholders(v), Placeholders(other)
			slices.Sort(a)
			slices.Sort(b)
			if !slices.Equal(a, b) {
				t.Errorf("%s: %s placeholders %v, want %v", lang, k, b, a)
			}
		}
		for k := range msgs {
			if _, ok := ref[k]; !ok {
				t.Errorf("%s: key %s does not exist in %s", lang, k, Default)
			}
		}
	}
}

func TestLocalizer(t *testing.T) {
	l, err := New("es")
	if err != nil {
		t.Fatal(err)
	}
	if l.Lang() != "es" {
		t.Errorf("Lang() = %q", l.Lang())
	}
	got := l.T("errors.GL0002.message", "command", "x")
	if got != "comando desconocido 'x'" {
		t.Errorf("T = %q", got)
	}
	if got := l.T("no.such.key"); got != "no.such.key" {
		t.Errorf("missing key returned %q", got)
	}
	if got := l.T("errors.GL0002.message", "command"); !strings.Contains(got, "{command}") {
		t.Errorf("odd arguments should leave placeholders untouched, got %q", got)
	}
	if _, err := New("xx"); !IsUnsupported(err) {
		t.Errorf("New(xx) error = %v", err)
	}
}

func TestPlaceholders(t *testing.T) {
	got := Placeholders("{a} and {b} then {a} but not {B} or {}")
	if !slices.Equal(got, []string{"{a}", "{b}"}) {
		t.Errorf("got %v", got)
	}
}

// TestKeysUsedInCodeExist parses the Go sources of the repository and checks
// that every literal key passed to a T method exists in the English catalog.
func TestKeysUsedInCodeExist(t *testing.T) {
	ref, err := Catalog(Default)
	if err != nil {
		t.Fatal(err)
	}
	root := repoRoot(t)
	fset := token.NewFileSet()
	checked := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "third_party", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "T" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			key, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			checked++
			if _, ok := ref[key]; !ok {
				t.Errorf("%s: key %q is not in the %s catalog", fset.Position(lit.Pos()), key, Default)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no translation calls found; the scan is not looking at the sources")
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
