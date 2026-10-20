// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package promtext parses the Prometheus text exposition format, which the
// inference backends serve on /metrics. Only samples are kept: names,
// labels and values. HELP and TYPE lines, timestamps and exemplars are
// skipped. The parser is strict about syntax and bounded in size, because
// the input comes from another process.
package promtext

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// Sample is one line of the format.
type Sample struct {
	Name   string
	Labels map[string]string
	Value  float64
}

// Limits bound what Parse accepts.
const (
	MaxLineBytes = 64 << 10
	MaxSamples   = 100_000
)

// Parse reads every sample from r.
func Parse(r io.Reader) ([]Sample, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), MaxLineBytes)
	var out []Sample
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		s, err := parseLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		out = append(out, s)
		if len(out) > MaxSamples {
			return nil, errors.New("too many samples")
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func parseLine(line string) (Sample, error) {
	var s Sample
	i := 0
	for i < len(line) && isNameChar(line[i], i == 0) {
		i++
	}
	if i == 0 {
		return s, errors.New("missing metric name")
	}
	s.Name = line[:i]
	rest := line[i:]
	if strings.HasPrefix(rest, "{") {
		labels, after, err := parseLabels(rest[1:])
		if err != nil {
			return s, err
		}
		s.Labels = labels
		rest = after
	}
	// OpenMetrics exemplars follow " # " and are not needed.
	rest, _, _ = strings.Cut(rest, " # ")
	fields := strings.Fields(rest)
	if len(fields) == 0 || len(fields) > 2 {
		return s, errors.New("expected a value and an optional timestamp")
	}
	v, err := parseValue(fields[0])
	if err != nil {
		return s, err
	}
	s.Value = v
	return s, nil
}

func isNameChar(c byte, first bool) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_', c == ':':
		return true
	case c >= '0' && c <= '9':
		return !first
	}
	return false
}

func parseLabels(s string) (map[string]string, string, error) {
	labels := map[string]string{}
	for {
		s = strings.TrimLeft(s, " ")
		if strings.HasPrefix(s, "}") {
			return labels, s[1:], nil
		}
		j := 0
		for j < len(s) && isNameChar(s[j], j == 0) && s[j] != ':' {
			j++
		}
		if j == 0 || j >= len(s) || s[j] != '=' || j+1 >= len(s) || s[j+1] != '"' {
			return nil, "", errors.New("malformed label")
		}
		name := s[:j]
		val, n, err := quoted(s[j+1:])
		if err != nil {
			return nil, "", err
		}
		labels[name] = val
		s = strings.TrimLeft(s[j+1+n:], " ")
		if strings.HasPrefix(s, ",") {
			s = s[1:]
			continue
		}
		if !strings.HasPrefix(s, "}") {
			return nil, "", errors.New("expected , or } after a label")
		}
	}
}

// quoted reads a double-quoted label value with the escapes of the format
// (\\, \" and \n) and returns it with the number of bytes consumed.
func quoted(s string) (string, int, error) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '"':
			return b.String(), i + 1, nil
		case '\\':
			if i+1 >= len(s) {
				return "", 0, errors.New("unterminated escape")
			}
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case '\\', '"':
				b.WriteByte(s[i])
			default:
				return "", 0, fmt.Errorf("invalid escape \\%c", s[i])
			}
		default:
			b.WriteByte(s[i])
		}
	}
	return "", 0, errors.New("unterminated label value")
}

func parseValue(s string) (float64, error) {
	switch s {
	case "+Inf":
		return math.Inf(1), nil
	case "-Inf":
		return math.Inf(-1), nil
	case "NaN":
		return math.NaN(), nil
	}
	return strconv.ParseFloat(s, 64)
}

// Find returns the samples named name whose labels include every pair of
// match.
func Find(samples []Sample, name string, match map[string]string) []Sample {
	var out []Sample
	for _, s := range samples {
		if s.Name != name {
			continue
		}
		ok := true
		for k, v := range match {
			if s.Labels[k] != v {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, s)
		}
	}
	return out
}

// Sum adds the values of the samples named name, grouped by the value of
// label (empty label: one group).
func Sum(samples []Sample, name, label string) map[string]float64 {
	out := map[string]float64{}
	for _, s := range samples {
		if s.Name == name && !math.IsNaN(s.Value) {
			out[s.Labels[label]] += s.Value
		}
	}
	return out
}

// Has reports whether any sample is named name.
func Has(samples []Sample, name string) bool {
	for _, s := range samples {
		if s.Name == name {
			return true
		}
	}
	return false
}
