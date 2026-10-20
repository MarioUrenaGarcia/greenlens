// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package output writes command results as a text table for people, or as
// JSON, CSV or YAML for scripts. JSON and YAML documents carry a
// schema_version; their field names do not change within a version.
package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"go.yaml.in/yaml/v3"
)

// Format is an output format.
type Format string

// Formats.
const (
	Table Format = "table"
	JSON  Format = "json"
	CSV   Format = "csv"
	YAML  Format = "yaml"
)

// Formats lists every format.
var Formats = []Format{Table, JSON, CSV, YAML}

// Parse validates a format name.
func Parse(s string) (Format, error) {
	for _, f := range Formats {
		if string(f) == s {
			return f, nil
		}
	}
	return "", fmt.Errorf("unknown output format %q", s)
}

// Grid is a table with a header row.
type Grid struct {
	Header []string
	Rows   [][]string
}

// Write renders doc in format f. For table and CSV, grid supplies the rows;
// CSV writes the first grid only, so a script always gets one table.
func Write(w io.Writer, f Format, doc any, grids ...Grid) error {
	switch f {
	case JSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	case YAML:
		// Going through JSON keeps the field names of the JSON document.
		b, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		out, err := yaml.Marshal(v)
		if err != nil {
			return err
		}
		_, err = w.Write(out)
		return err
	case CSV:
		if len(grids) == 0 {
			return nil
		}
		cw := csv.NewWriter(w)
		if err := cw.Write(grids[0].Header); err != nil {
			return err
		}
		if err := cw.WriteAll(grids[0].Rows); err != nil {
			return err
		}
		cw.Flush()
		return cw.Error()
	default:
		for i, g := range grids {
			if i > 0 {
				if _, err := io.WriteString(w, "\n"); err != nil {
					return err
				}
			}
			if err := WriteGrid(w, g); err != nil {
				return err
			}
		}
		return nil
	}
}

// WriteGrid writes an aligned text table.
func WriteGrid(w io.Writer, g Grid) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if len(g.Header) > 0 {
		if _, err := fmt.Fprintln(tw, strings.Join(g.Header, "\t")); err != nil {
			return err
		}
	}
	for _, r := range g.Rows {
		if _, err := fmt.Fprintln(tw, strings.Join(r, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}
