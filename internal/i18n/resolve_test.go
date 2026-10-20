// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package i18n

import (
	"errors"
	"testing"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		name    string
		flag    string
		env     map[string]string
		want    string
		wantErr bool
	}{
		{name: "nothing set", want: "en"},
		{name: "flag wins", flag: "es", env: map[string]string{"GREENLENS_LANG": "en", "LANG": "en_US.UTF-8"}, want: "es"},
		{name: "env over locale", env: map[string]string{"GREENLENS_LANG": "es", "LANG": "en_US.UTF-8"}, want: "es"},
		{name: "LC_ALL over LANG", env: map[string]string{"LC_ALL": "es_ES.UTF-8", "LANG": "en_US.UTF-8"}, want: "es"},
		{name: "LC_MESSAGES over LANG", env: map[string]string{"LC_MESSAGES": "es_MX", "LANG": "en_US"}, want: "es"},
		{name: "first set locale decides", env: map[string]string{"LC_ALL": "fr_FR.UTF-8", "LANG": "es_MX.UTF-8"}, want: "en"},
		{name: "C locale", env: map[string]string{"LANG": "C.UTF-8"}, want: "en"},
		{name: "modifier and dash", env: map[string]string{"LANG": "es-419@euro"}, want: "es"},
		{name: "auto flag uses locale", flag: "auto", env: map[string]string{"LANG": "es_MX.UTF-8"}, want: "es"},
		{name: "unsupported flag", flag: "fr", env: map[string]string{"LANG": "es_MX.UTF-8"}, want: "es", wantErr: true},
		{name: "unsupported env", env: map[string]string{"GREENLENS_LANG": "de"}, want: "en", wantErr: true},
		{name: "case insensitive", flag: "ES", want: "es"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.flag, func(k string) string { return tt.env[k] })
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Resolve = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveReportsSource(t *testing.T) {
	_, err := Resolve("", func(k string) string {
		if k == SourceEnv {
			return "xx"
		}
		return ""
	})
	var u *UnsupportedError
	if !errors.As(err, &u) || u.Source != SourceEnv || u.Value != "xx" {
		t.Fatalf("error = %#v", err)
	}
}
