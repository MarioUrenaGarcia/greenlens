// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package manpage_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MarioUrenaGarcia/greenlens/internal/cli"
	"github.com/MarioUrenaGarcia/greenlens/internal/i18n"
	"github.com/MarioUrenaGarcia/greenlens/internal/manpage"
)

func pages(t *testing.T, lang string) []manpage.Page {
	t.Helper()
	l, err := i18n.New(lang)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := cli.Tree(lang)
	if err != nil {
		t.Fatal(err)
	}
	h := manpage.Header{Date: "2026-10-20", Version: "v0.3.0"}
	return append(manpage.Commands(tree, l, h), manpage.Config(l, h))
}

func TestPages(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		ps := pages(t, lang)
		names := map[string]string{}
		for _, p := range ps {
			names[p.Name] = p.Content
		}
		for _, want := range []string{"greenlens.1", "greenlens-status.1", "greenlens-config-validate.1", "greenlens-top.1", "greenlens.yaml.5"} {
			if _, ok := names[want]; !ok {
				t.Errorf("%s: no page %s", lang, want)
			}
		}
		for name, c := range names {
			if !strings.HasPrefix(c, ".TH ") || !strings.Contains(c, "\n.SH ") {
				t.Errorf("%s %s: not a manual page", lang, name)
			}
			for i, b := range []byte(c) {
				if b > 0x7e && b != '\n' {
					t.Errorf("%s %s: byte %#x at %d is not ASCII", lang, name, b, i)
					break
				}
			}
			for _, line := range strings.Split(c, "\n") {
				if strings.HasPrefix(line, ".") && !strings.HasPrefix(line, ".TH") && !strings.HasPrefix(line, ".SH") &&
					!strings.HasPrefix(line, ".TP") && !strings.HasPrefix(line, ".B ") && !strings.HasPrefix(line, ".nf") &&
					!strings.HasPrefix(line, ".fi") && !strings.HasPrefix(line, ".br") {
					t.Errorf("%s %s: stray request %q", lang, name, line)
				}
			}
		}
		if es := names["greenlens-top.1"]; lang == "es" && !strings.Contains(es, `DESCRIPCI\[u00D3]N`) {
			t.Errorf("Spanish headings are not escaped:\n%s", es)
		}
		if !strings.Contains(names["greenlens-bench.1"], `\fB\-\-max\-tokens\fR \fIint\fR`) {
			t.Errorf("options are not written as roff:\n%s", names["greenlens-bench.1"])
		}
		if !strings.Contains(names["greenlens.yaml.5"], `GREENLENS_AGENT_SAMPLE_INTERVAL`) {
			t.Error("the configuration page lacks the variables")
		}
	}
}

// TestGroff checks every page with groff when it is installed.
func TestGroff(t *testing.T) {
	groff, err := exec.LookPath("groff")
	if err != nil {
		t.Skip("groff is not installed")
	}
	dir := t.TempDir()
	for _, lang := range []string{"en", "es"} {
		for _, p := range pages(t, lang) {
			path := filepath.Join(dir, lang+"-"+p.Name)
			if err := os.WriteFile(path, []byte(p.Content), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(groff, "-man", "-z", "-ww", path).CombinedOutput() //nolint:gosec,noctx // a file the test wrote
			if err != nil || len(out) > 0 {
				t.Errorf("%s %s: %v\n%s", lang, p.Name, err, out)
			}
		}
	}
}
