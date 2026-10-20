// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func run(t *testing.T, env map[string]string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Execute(context.Background(), args, &out, &errOut, func(k string) string { return env[k] })
	return code, out.String(), errOut.String()
}

func TestExecute(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		args       []string
		wantCode   int
		wantOut    []string
		wantErr    []string
		wantNoText []string
	}{
		{name: "no arguments shows help", wantOut: []string{"Usage:", "Available commands:", "version"}},
		{name: "version", args: []string{"version"}, wantOut: []string{"greenlens ", "Go version:", "Platform:"}},
		{
			name: "unknown command with suggestion", args: []string{"verson"}, wantCode: 2,
			wantErr: []string{"error GL0002: unknown command 'verson'", "did you mean 'version'?"},
		},
		{name: "unknown flag", args: []string{"version", "--nope"}, wantCode: 2, wantErr: []string{"GL0003", "'--nope'"}},
		{name: "unknown shorthand", args: []string{"version", "-z"}, wantCode: 2, wantErr: []string{"GL0003", "'-z'"}},
		{name: "flag without value", args: []string{"--lang"}, wantCode: 2, wantErr: []string{"GL0004", "'--lang'"}},
		{name: "flag of the wrong type", args: []string{"version", "--help=maybe"}, wantCode: 2, wantErr: []string{"GL0005", "'maybe'"}},
		{name: "bad flag syntax", args: []string{"version", "--=x"}, wantCode: 2, wantErr: []string{"GL0006"}},
		{name: "unexpected arguments", args: []string{"version", "extra"}, wantCode: 2, wantErr: []string{"GL0007", "extra"}},
		{name: "unsupported language", args: []string{"--lang", "fr"}, wantCode: 2, wantErr: []string{"GL0008", "en, es"}},
		{name: "help for a command", args: []string{"help", "version"}, wantOut: []string{"greenlens version [flags]", "Examples:"}},
		{name: "help for an unknown command", args: []string{"help", "nope"}, wantCode: 2, wantErr: []string{"GL0002"}},
		{
			name: "spanish from the locale", env: map[string]string{"LANG": "es_MX.UTF-8"}, args: []string{"--help"},
			wantOut:    []string{"Uso:", "Comandos disponibles:", "Opciones:", "[opciones]"},
			wantNoText: []string{"Usage:", "Flags:", "[flags]", "help for", "Help about"},
		},
		{
			name: "spanish errors", args: []string{"--lang=es", "verson"}, wantCode: 2,
			wantErr: []string{"comando desconocido", "¿quisiste decir 'version'?"},
		},
		{
			name: "unsupported language reported in the locale language", env: map[string]string{"LANG": "es_MX.UTF-8"},
			args: []string{"--lang", "fr"}, wantCode: 2, wantErr: []string{"idioma no soportado 'fr'"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out, errOut := run(t, tt.env, tt.args...)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tt.wantCode, out, errOut)
			}
			for _, s := range tt.wantOut {
				if !strings.Contains(out, s) {
					t.Errorf("stdout lacks %q:\n%s", s, out)
				}
			}
			for _, s := range tt.wantErr {
				if !strings.Contains(errOut, s) {
					t.Errorf("stderr lacks %q:\n%s", s, errOut)
				}
			}
			for _, s := range tt.wantNoText {
				if strings.Contains(out+errOut, s) {
					t.Errorf("output contains untranslated %q:\n%s%s", s, out, errOut)
				}
			}
		})
	}
}

func TestLangFlag(t *testing.T) {
	tests := map[string][]string{
		"es": {"version", "--lang=es"},
		"en": {"--lang", "en", "version"},
		"":   {"version", "--", "--lang", "es"},
	}
	for want, args := range tests {
		if got := langFlag(args); got != want {
			t.Errorf("langFlag(%v) = %q, want %q", args, got, want)
		}
	}
	if got := langFlag([]string{"--lang"}); got != "" {
		t.Errorf("dangling --lang = %q", got)
	}
}
