// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWrap(t *testing.T) {
	tests := []struct {
		in    string
		width int
		want  string
	}{
		{"a b c", 3, "a b\nc"},
		{"energía térmica útil", 10, "energía\ntérmica\nútil"},
		{"keep\n\nparagraphs", 80, "keep\n\nparagraphs"},
		{"averyveryverylongword x", 5, "averyveryverylongword\nx"},
		{"  spaced   out  ", 80, "spaced out"},
	}
	for _, tt := range tests {
		if got := wrap(tt.in, tt.width); got != tt.want {
			t.Errorf("wrap(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
		}
	}
}

func TestHelpFitsEightyColumns(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		for _, args := range [][]string{{"--help"}, {"version", "--help"}, {"help", "help"}} {
			_, out, _ := run(t, map[string]string{"GREENLENS_LANG": lang}, args...)
			for _, l := range strings.Split(out, "\n") {
				if utf8.RuneCountInString(l) > helpWidth {
					t.Errorf("%s %v: line of %d columns: %q", lang, args, utf8.RuneCountInString(l), l)
				}
			}
		}
	}
}
