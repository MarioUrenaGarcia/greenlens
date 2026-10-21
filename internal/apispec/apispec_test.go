// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package apispec

import (
	"slices"
	"strings"
	"testing"

	"github.com/MarioUrenaGarcia/greenlens"
)

func spec(t *testing.T) *Spec {
	t.Helper()
	s, err := Load(greenlens.OpenAPI)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestValidate(t *testing.T) {
	s := spec(t)
	tok := map[string]any{"$ref": "#/components/schemas/Token"}
	for _, tt := range []struct {
		doc  string
		want string
	}{
		{`{"id":"0123456789ab","name":"ci","role":"viewer","created_at":"2026-10-20T10:00:00Z"}`, ""},
		{`{"id":"0123456789ab","name":"ci","role":"viewer","created_at":"2026-10-20T10:00:00Z","expires_at":"2027-01-01T00:00:00.5-06:00"}`, ""},
		{`{"id":"0123456789ab","name":"ci","role":"viewer"}`, "missing created_at"},
		{`{"id":"0123456789ab","name":"ci","role":"root","created_at":"2026-10-20T10:00:00Z"}`, "is not one of"},
		{`{"id":"xyz","name":"ci","role":"viewer","created_at":"2026-10-20T10:00:00Z"}`, "does not match"},
		{`{"id":"0123456789ab","name":"ci","role":"viewer","created_at":"yesterday"}`, "RFC 3339"},
		{`{"id":"0123456789ab","name":"ci","role":"viewer","created_at":"2026-10-20T10:00:00Z","hash":"x"}`, "hash is not described"},
		{`{"id":7,"name":"ci","role":"viewer","created_at":"2026-10-20T10:00:00Z"}`, "is not of type"},
	} {
		err := s.Validate(tok, []byte(tt.doc))
		if tt.want == "" && err != nil || tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
			t.Errorf("%s: %v", tt.doc, err)
		}
	}
	live := map[string]any{"$ref": "#/components/schemas/Live"}
	if err := s.Validate(live, []byte(`{"schema_version":1,"points":[{"time":"2026-10-20T10:00:00Z","interval_ms":1000.5,"power_w":{"package":3}}]}`)); err == nil {
		t.Error("a fractional integer was accepted")
	}
	if err := s.Validate(live, []byte(`{"schema_version":1,"points":[{"time":"2026-10-20T10:00:00Z","interval_ms":1000,"power_w":{"package":"3"}}]}`)); err == nil {
		t.Error("a string in a map of numbers was accepted")
	}
}

func TestEveryDescriptionIsTranslated(t *testing.T) {
	s := spec(t)
	var walk func(v any, at string)
	walk = func(v any, at string) {
		switch x := v.(type) {
		case map[string]any:
			// A field may be named description; only text is a description.
			if _, ok := x["description"].(string); ok {
				if es, _ := x["x-description-es"].(string); es == "" {
					t.Errorf("%s has no x-description-es", at)
				}
			}
			if _, ok := x["summary"].(string); ok {
				if es, _ := x["x-summary-es"].(string); es == "" {
					t.Errorf("%s has no x-summary-es", at)
				}
			}
			for k, e := range x {
				walk(e, at+"/"+k)
			}
		case []any:
			for _, e := range x {
				walk(e, at)
			}
		}
	}
	walk(s.Doc, "#")
	for _, op := range s.Operations() {
		if op.ID == "" || !slices.Contains([]string{"public", "viewer", "operator", "admin"}, op.Role) {
			t.Errorf("%s %s: id %q role %q", op.Method, op.Path, op.ID, op.Role)
		}
	}
	for name, v := range s.Schemas() {
		m, _ := v.(map[string]any)
		if m["type"] == "object" && m["additionalProperties"] == nil {
			t.Errorf("schema %s does not say whether other fields are allowed", name)
		}
	}
}
