// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package i18n loads the translation catalogs and resolves the language used
// for every message shown by the command line, the terminal interface and the
// graphical interface.
package i18n

import (
	"bufio"
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

//go:embed locales/*.toml
var localeFS embed.FS

// Default is the language used when no preference names a supported language.
const Default = "en"

// Supported lists the languages that ship a complete catalog.
var Supported = []string{"en", "es"}

// The catalog format is a strict subset of TOML: comments, [table] headers and
// key = "basic string" lines. Keeping it this small lets scripts/check-i18n.sh
// validate the same grammar with awk, without a TOML library.
var (
	tableLine   = regexp.MustCompile(`^\[([A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)*)\]$`)
	entryLine   = regexp.MustCompile(`^([A-Za-z0-9_]+)[ \t]*=[ \t]*(".*")$`)
	placeholder = regexp.MustCompile(`\{[a-z_]+\}`)
)

// ParseCatalog parses one catalog file into a map of dotted keys to messages.
func ParseCatalog(data []byte) (map[string]string, error) {
	msgs := make(map[string]string)
	table := ""
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if m := tableLine.FindStringSubmatch(line); m != nil {
			table = m[1]
			continue
		}
		m := entryLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("line %d: expected a comment, a [table] header or key = \"value\"", n)
		}
		value, err := strconv.Unquote(m[2])
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid string: %w", n, err)
		}
		key := m[1]
		if table != "" {
			key = table + "." + key
		}
		if _, dup := msgs[key]; dup {
			return nil, fmt.Errorf("line %d: duplicate key %q", n, key)
		}
		msgs[key] = value
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return msgs, nil
}

// Placeholders returns the {name} placeholders used by a message, in order of
// appearance and without duplicates.
func Placeholders(msg string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, p := range placeholder.FindAllString(msg, -1) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

var loadCatalogs = sync.OnceValues(func() (map[string]map[string]string, error) {
	catalogs := make(map[string]map[string]string, len(Supported))
	for _, lang := range Supported {
		name := path.Join("locales", lang+".toml")
		data, err := fs.ReadFile(localeFS, name)
		if err != nil {
			return nil, err
		}
		msgs, err := ParseCatalog(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		catalogs[lang] = msgs
	}
	return catalogs, nil
})

// Catalog returns a copy of the messages of one supported language.
func Catalog(lang string) (map[string]string, error) {
	catalogs, err := loadCatalogs()
	if err != nil {
		return nil, err
	}
	msgs, ok := catalogs[lang]
	if !ok {
		return nil, &UnsupportedError{Value: lang}
	}
	out := make(map[string]string, len(msgs))
	for k, v := range msgs {
		out[k] = v
	}
	return out, nil
}

// Localizer returns messages in one language. Keys missing from that language
// fall back to English, and keys missing everywhere are returned unchanged so
// the gap is visible instead of producing an empty string.
type Localizer struct {
	lang     string
	messages map[string]string
	fallback map[string]string
}

// New returns a Localizer for a supported language.
func New(lang string) (*Localizer, error) {
	catalogs, err := loadCatalogs()
	if err != nil {
		return nil, err
	}
	msgs, ok := catalogs[lang]
	if !ok {
		return nil, &UnsupportedError{Value: lang}
	}
	return &Localizer{lang: lang, messages: msgs, fallback: catalogs[Default]}, nil
}

// Lang returns the language code of the Localizer.
func (l *Localizer) Lang() string { return l.lang }

// Has reports whether the Localizer's own language defines key, without
// considering the English fallback.
func (l *Localizer) Has(key string) bool {
	_, ok := l.messages[key]
	return ok
}

// T returns the message for key with its {name} placeholders replaced by the
// name/value pairs in args.
func (l *Localizer) T(key string, args ...any) string {
	msg, ok := l.messages[key]
	if !ok {
		msg, ok = l.fallback[key]
	}
	if !ok {
		return key
	}
	if len(args) < 2 {
		return msg
	}
	pairs := make([]string, 0, len(args))
	for i := 0; i+1 < len(args); i += 2 {
		name, ok := args[i].(string)
		if !ok {
			continue
		}
		pairs = append(pairs, "{"+name+"}", fmt.Sprint(args[i+1]))
	}
	return strings.NewReplacer(pairs...).Replace(msg)
}

// UnsupportedError reports a language preference that has no catalog.
type UnsupportedError struct {
	Value  string
	Source string
}

func (e *UnsupportedError) Error() string {
	if e.Source == "" {
		return fmt.Sprintf("unsupported language %q", e.Value)
	}
	return fmt.Sprintf("unsupported language %q from %s", e.Value, e.Source)
}

// IsUnsupported reports whether err is an UnsupportedError.
func IsUnsupported(err error) bool {
	var u *UnsupportedError
	return errors.As(err, &u)
}
