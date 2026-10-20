// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package output

import (
	"bytes"
	"strings"
	"testing"
)

type doc struct {
	SchemaVersion int    `json:"schema_version"`
	Name          string `json:"name"`
}

func TestWrite(t *testing.T) {
	g := Grid{Header: []string{"A", "BB"}, Rows: [][]string{{"x", "1"}, {"long, value", "22"}}}
	d := doc{SchemaVersion: 1, Name: "node"}
	tests := map[Format]string{
		JSON:  "{\n  \"schema_version\": 1,\n  \"name\": \"node\"\n}\n",
		YAML:  "name: node\nschema_version: 1\n",
		CSV:   "A,BB\nx,1\n\"long, value\",22\n",
		Table: "A            BB\nx            1\nlong, value  22\n",
	}
	for f, want := range tests {
		var b bytes.Buffer
		if err := Write(&b, f, d, g); err != nil {
			t.Fatal(err)
		}
		if b.String() != want {
			t.Errorf("%s:\n%q\nwant\n%q", f, b.String(), want)
		}
	}
	var b bytes.Buffer
	if err := Write(&b, Table, d, g, g); err != nil || strings.Count(b.String(), "BB") != 2 || !strings.Contains(b.String(), "\n\n") {
		t.Errorf("two tables: %q %v", b.String(), err)
	}
}

func TestParse(t *testing.T) {
	for _, f := range Formats {
		if got, err := Parse(string(f)); err != nil || got != f {
			t.Errorf("Parse(%q)", f)
		}
	}
	if _, err := Parse("xml"); err == nil {
		t.Error("xml accepted")
	}
}
