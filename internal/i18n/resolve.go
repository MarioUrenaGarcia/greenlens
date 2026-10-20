// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package i18n

import (
	"slices"
	"strings"
)

// Auto selects the system locale when given as an explicit preference.
const Auto = "auto"

// Sources of an explicit language preference, used in error messages.
const (
	SourceFlag = "--lang"
	SourceEnv  = "GREENLENS_LANG"
)

// Resolve picks the language for messages. An explicit preference, the --lang
// flag first and then GREENLENS_LANG, must name a supported language. Without
// one, the POSIX locale variables are consulted in their standard order
// (LC_ALL, LC_MESSAGES, LANG): the first one that is set decides, and an
// unsupported or C locale falls back to English.
//
// When the explicit preference is unsupported, Resolve returns the language of
// the system locale together with the error, so the error itself can be shown
// in a language the user reads.
func Resolve(flag string, getenv func(string) string) (string, error) {
	explicit := []struct{ value, source string }{
		{flag, SourceFlag},
		{getenv(SourceEnv), SourceEnv},
	}
	for _, e := range explicit {
		v := strings.TrimSpace(e.value)
		if v == "" || strings.EqualFold(v, Auto) {
			continue
		}
		lang, ok := normalize(v)
		if !ok {
			return systemLanguage(getenv), &UnsupportedError{Value: v, Source: e.source}
		}
		return lang, nil
	}
	return systemLanguage(getenv), nil
}

func systemLanguage(getenv func(string) string) string {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		v := getenv(name)
		if v == "" {
			continue
		}
		if lang, ok := normalize(v); ok {
			return lang
		}
		return Default
	}
	return Default
}

// normalize reduces a locale such as "es_MX.UTF-8@euro" or "pt-BR" to its
// language code and reports whether that language is supported.
func normalize(locale string) (string, bool) {
	l := strings.ToLower(locale)
	if i := strings.IndexAny(l, ".@"); i >= 0 {
		l = l[:i]
	}
	if i := strings.IndexAny(l, "_-"); i >= 0 {
		l = l[:i]
	}
	if l == "c" || l == "posix" {
		return Default, true
	}
	if slices.Contains(Supported, l) {
		return l, true
	}
	return "", false
}
