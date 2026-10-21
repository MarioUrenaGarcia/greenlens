// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package help holds the catalog of error codes and the rendering shared by
// every interface: a one-line message, the cause and how to fix it.
package help

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/MarioUrenaGarcia/greenlens/internal/i18n"
)

// Code identifies an error condition. Codes are stable across releases so
// that documentation, scripts and support requests can refer to them.
type Code string

// Error codes. GL0xxx covers the command line and general errors, GL1xxx
// the energy sources, GL3xxx the configuration and GL4xxx the agent and its
// storage.
const (
	GL0001 Code = "GL0001" // unexpected internal error
	GL0002 Code = "GL0002" // unknown command
	GL0003 Code = "GL0003" // unknown flag
	GL0004 Code = "GL0004" // flag without its value
	GL0005 Code = "GL0005" // flag value of the wrong type
	GL0006 Code = "GL0006" // malformed flag
	GL0007 Code = "GL0007" // positional arguments where none are accepted
	GL0008 Code = "GL0008" // unsupported language
	GL0009 Code = "GL0009" // unsupported output format
	GL0010 Code = "GL0010" // unknown error code asked to help
	GL0011 Code = "GL0011" // command to measure cannot start
	GL0012 Code = "GL0012" // measure needs exactly one target
	GL0013 Code = "GL0013" // process to measure not found
	GL0014 Code = "GL0014" // cgroup of the unit not found
	GL0015 Code = "GL0015" // wrong number of positional arguments
	GL0016 Code = "GL0016" // the terminal interface needs a terminal
	GL0017 Code = "GL0017" // unknown metric asked to explain

	GL1001 Code = "GL1001" // no measured energy source
	GL1002 Code = "GL1002" // RAPL counters readable only by root
	GL1003 Code = "GL1003" // perf_event energy counters not permitted
	GL1004 Code = "GL1004" // IPMI device not accessible
	GL1005 Code = "GL1005" // hwmon sensor not readable
	GL1006 Code = "GL1006" // BMC without power readings
	GL1007 Code = "GL1007" // CPU energy is estimated
	GL1008 Code = "GL1008" // counter can wrap more than once per sample
	GL1009 Code = "GL1009" // hwmon power sensor of an unknown driver
	GL1010 Code = "GL1010" // processor not in the estimation table
	GL1011 Code = "GL1011" // permission changes need root
	GL1012 Code = "GL1012" // a permission change failed
	GL1013 Code = "GL1013" // NVIDIA GPU without the NVML helper
	GL1014 Code = "GL1014" // NVML cannot be used
	GL1015 Code = "GL1015" // nvidia-smi cannot be used

	GL2001 Code = "GL2001" // backend not reachable
	GL2002 Code = "GL2002" // backend without metrics
	GL2003 Code = "GL2003" // backend refused a benchmark request

	GL3001 Code = "GL3001" // configuration file not found
	GL3002 Code = "GL3002" // configuration file cannot be read or parsed
	GL3003 Code = "GL3003" // invalid configuration value
	GL3004 Code = "GL3004" // invalid configuration environment variable

	GL4001 Code = "GL4001" // agent not reachable
	GL4002 Code = "GL4002" // database cannot be opened
	GL4003 Code = "GL4003" // agent socket cannot be created
	GL4004 Code = "GL4004" // no runtime directory for the socket
	GL4005 Code = "GL4005" // another agent already listens
	GL4006 Code = "GL4006" // the agent rejected a request
	GL4007 Code = "GL4007" // backup file cannot be written
	GL4008 Code = "GL4008" // file is not a valid backup
	GL4009 Code = "GL4009" // restore refused while the agent runs
	GL4010 Code = "GL4010" // backup made by a newer version
)

// Process exit codes of the command line.
const (
	ExitOK         = 0
	ExitError      = 1
	ExitUsage      = 2
	ExitNoSource   = 3
	ExitBackend    = 4
	ExitPermission = 5
	ExitConfig     = 6
)

var exitCodes = map[Code]int{
	GL0001: ExitError,
	GL0002: ExitUsage,
	GL0003: ExitUsage,
	GL0004: ExitUsage,
	GL0005: ExitUsage,
	GL0006: ExitUsage,
	GL0007: ExitUsage,
	GL0008: ExitUsage,
	GL0009: ExitUsage,
	GL0010: ExitUsage,
	GL0011: ExitError,
	GL0012: ExitUsage,
	GL0013: ExitError,
	GL0014: ExitError,
	GL0015: ExitUsage,
	GL0016: ExitUsage,
	GL0017: ExitUsage,
	GL1001: ExitNoSource,
	GL1002: ExitPermission,
	GL1003: ExitPermission,
	GL1004: ExitPermission,
	GL1005: ExitPermission,
	GL1006: ExitNoSource,
	GL1007: ExitNoSource,
	GL1008: ExitError,
	GL1009: ExitNoSource,
	GL1010: ExitNoSource,
	GL1011: ExitPermission,
	GL1012: ExitPermission,
	GL1013: ExitNoSource,
	GL1014: ExitNoSource,
	GL1015: ExitNoSource,
	GL2001: ExitBackend,
	GL2002: ExitBackend,
	GL2003: ExitBackend,
	GL3001: ExitConfig,
	GL3002: ExitConfig,
	GL3003: ExitConfig,
	GL3004: ExitConfig,
	GL4001: ExitError,
	GL4002: ExitError,
	GL4003: ExitError,
	GL4004: ExitConfig,
	GL4005: ExitError,
	GL4006: ExitError,
	GL4007: ExitError,
	GL4008: ExitError,
	GL4009: ExitError,
	GL4010: ExitError,
}

// IsCode reports whether s looks like an error code, such as GL1002.
func IsCode(s string) bool {
	return len(s) == 6 && strings.EqualFold(s[:2], "GL") && strings.Trim(s[2:], "0123456789") == ""
}

// Known reports whether c is a registered code.
func Known(c Code) bool {
	_, ok := exitCodes[c]
	return ok
}

// Codes returns every registered error code.
func Codes() []Code {
	out := make([]Code, 0, len(exitCodes))
	for c := range exitCodes {
		out = append(out, c)
	}
	return out
}

// Error is an error with a stable code whose message, cause and fix come from
// the translation catalogs under errors.<code>.
type Error struct {
	Code Code
	// Args holds name/value pairs for the {placeholders} of the messages.
	Args []any
	// FixKey, when set, names the catalog key under errors.<code> used for
	// the fix instead of "fix".
	FixKey string
	// Err is the underlying error, if any.
	Err error
}

// New returns an Error for code with the given name/value pairs.
func New(code Code, args ...any) *Error {
	return &Error{Code: code, Args: args}
}

// WithFix selects an alternative fix message, such as "fix_suggest".
func (e *Error) WithFix(key string) *Error {
	e.FixKey = key
	return e
}

// Wrap records the underlying error.
func (e *Error) Wrap(err error) *Error {
	e.Err = err
	return e
}

// Error returns the code, the English message and the cause, for logs.
func (e *Error) Error() string {
	l, err := i18n.New(i18n.Default)
	if err != nil {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Message(l) + ": " + e.Cause(l)
}

// Unwrap returns the underlying error.
func (e *Error) Unwrap() error { return e.Err }

// ExitCode returns the process exit code associated with the error code.
func (e *Error) ExitCode() int {
	if c, ok := exitCodes[e.Code]; ok {
		return c
	}
	return ExitError
}

// Message returns the one-line message in the Localizer's language.
func (e *Error) Message(l *i18n.Localizer) string {
	return l.T(e.key("message"), e.Args...)
}

// Cause returns the cause in the Localizer's language.
func (e *Error) Cause(l *i18n.Localizer) string {
	return l.T(e.key("cause"), e.Args...)
}

// Fix returns the suggested fix in the Localizer's language.
func (e *Error) Fix(l *i18n.Localizer) string {
	name := "fix"
	if e.FixKey != "" {
		name = e.FixKey
	}
	return l.T(e.key(name), e.Args...)
}

func (e *Error) key(name string) string {
	return "errors." + string(e.Code) + "." + name
}

// As returns err as an *Error. Errors without a code become GL0001 with the
// original text as the cause, so every failure is reported in the same form.
func As(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return New(GL0001, "detail", err.Error()).Wrap(err)
}

// ExitCode returns the process exit code for err; nil means success.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	return As(err).ExitCode()
}

// Render formats err for a terminal:
//
//	error GL1002: cannot read RAPL energy counters
//	  cause: ...
//	  fix:   ...
func Render(l *i18n.Localizer, err error) string {
	e := As(err)
	causeLabel := l.T("errors.cause")
	fixLabel := l.T("errors.fix")
	width := max(utf8.RuneCountInString(causeLabel), utf8.RuneCountInString(fixLabel))
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s: %s\n", l.T("errors.label"), e.Code, e.Message(l))
	fmt.Fprintf(&b, "  %s %s\n", pad(causeLabel, width), e.Cause(l))
	fmt.Fprintf(&b, "  %s %s", pad(fixLabel, width), e.Fix(l))
	return b.String()
}

// RenderAll formats every coded error inside err, for errors that join
// several problems such as an invalid configuration file.
func RenderAll(l *i18n.Localizer, err error) string {
	var parts []string
	for _, e := range Flatten(err) {
		parts = append(parts, Render(l, e))
	}
	return strings.Join(parts, "\n")
}

// Flatten returns the errors joined in err, or err itself.
func Flatten(err error) []error {
	if err == nil {
		return nil
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		var out []error
		for _, e := range j.Unwrap() {
			out = append(out, Flatten(e)...)
		}
		return out
	}
	return []error{err}
}

func pad(s string, width int) string {
	return s + strings.Repeat(" ", width-utf8.RuneCountInString(s))
}
