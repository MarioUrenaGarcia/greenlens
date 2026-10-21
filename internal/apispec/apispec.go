// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package apispec reads the OpenAPI description of the agent and checks
// JSON documents against its schemas. It covers the part of JSON Schema the
// description uses: type, properties, required, additionalProperties,
// items, enum, pattern, minimum, maximum, oneOf, format date-time and $ref
// to #/components/schemas.
package apispec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Spec is a parsed OpenAPI document.
type Spec struct {
	Doc map[string]any
}

// Operation is one method of one path.
type Operation struct {
	Method, Path string
	ID           string
	Role         string
	SocketOnly   bool
	Raw          map[string]any
}

// Load parses an OpenAPI document written in YAML.
func Load(data []byte) (*Spec, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if v, _ := doc["openapi"].(string); !strings.HasPrefix(v, "3.1.") {
		return nil, fmt.Errorf("openapi version %q, want 3.1", v)
	}
	return &Spec{Doc: doc}, nil
}

var methods = []string{"get", "put", "post", "delete", "patch", "head", "options"}

// Operations lists every operation, sorted by path and method.
func (s *Spec) Operations() []Operation {
	paths, _ := s.Doc["paths"].(map[string]any)
	var out []Operation
	for p, v := range paths {
		item, _ := v.(map[string]any)
		for _, m := range methods {
			op, ok := item[m].(map[string]any)
			if !ok {
				continue
			}
			o := Operation{Method: strings.ToUpper(m), Path: p, Raw: op}
			o.ID, _ = op["operationId"].(string)
			o.Role, _ = op["x-greenlens-role"].(string)
			o.SocketOnly, _ = op["x-greenlens-socket-only"].(bool)
			out = append(out, o)
		}
	}
	slices.SortFunc(out, func(a, b Operation) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		return slices.Index(methods, strings.ToLower(a.Method)) - slices.Index(methods, strings.ToLower(b.Method))
	})
	return out
}

// Schemas returns the named schemas of the components.
func (s *Spec) Schemas() map[string]any {
	comp, _ := s.Doc["components"].(map[string]any)
	schemas, _ := comp["schemas"].(map[string]any)
	return schemas
}

// ResponseSchema returns the JSON schema of the answer of an operation with
// status, following a reference to a shared response.
func (s *Spec) ResponseSchema(op Operation, status string) (map[string]any, bool) {
	rs, _ := op.Raw["responses"].(map[string]any)
	r, ok := rs[status].(map[string]any)
	if !ok {
		return nil, false
	}
	if ref, ok := r["$ref"].(string); ok {
		name, found := strings.CutPrefix(ref, "#/components/responses/")
		if !found {
			return nil, false
		}
		comp, _ := s.Doc["components"].(map[string]any)
		all, _ := comp["responses"].(map[string]any)
		if r, ok = all[name].(map[string]any); !ok {
			return nil, false
		}
	}
	content, _ := r["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	schema, ok := media["schema"].(map[string]any)
	return schema, ok
}

// HasStatus reports whether an operation documents status.
func (op Operation) HasStatus(status string) bool {
	rs, _ := op.Raw["responses"].(map[string]any)
	_, ok := rs[status]
	return ok
}

// Validate checks a JSON document against schema.
func (s *Spec) Validate(schema map[string]any, data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return err
	}
	return s.check(schema, v, "$")
}

func (s *Spec) resolve(schema map[string]any) (map[string]any, error) {
	for range 16 {
		ref, ok := schema["$ref"].(string)
		if !ok {
			return schema, nil
		}
		name, found := strings.CutPrefix(ref, "#/components/schemas/")
		target, ok2 := s.Schemas()[name].(map[string]any)
		if !found || !ok2 {
			return nil, fmt.Errorf("unknown reference %s", ref)
		}
		schema = target
	}
	return nil, errors.New("references nest too deeply")
}

func (s *Spec) check(schema map[string]any, v any, at string) error {
	schema, err := s.resolve(schema)
	if err != nil {
		return fmt.Errorf("%s: %w", at, err)
	}
	if alts, ok := schema["oneOf"].([]any); ok {
		var errs []error
		for _, a := range alts {
			sub, _ := a.(map[string]any)
			e := s.check(sub, v, at)
			if e == nil {
				return nil
			}
			errs = append(errs, e)
		}
		return fmt.Errorf("%s matches none of the alternatives: %w", at, errors.Join(errs...))
	}
	if t, ok := schema["type"]; ok {
		if !typeMatches(t, v) {
			return fmt.Errorf("%s: %s is not of type %v", at, describe(v), t)
		}
	}
	if enum, ok := schema["enum"].([]any); ok && !slices.ContainsFunc(enum, func(e any) bool { return fmt.Sprint(e) == fmt.Sprint(v) }) {
		return fmt.Errorf("%s: %v is not one of %v", at, v, enum)
	}
	switch x := v.(type) {
	case map[string]any:
		return s.checkObject(schema, x, at)
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for i, e := range x {
				if err := s.check(items, e, fmt.Sprintf("%s[%d]", at, i)); err != nil {
					return err
				}
			}
		}
	case string:
		if p, ok := schema["pattern"].(string); ok {
			re, err := regexp.Compile(p)
			if err != nil {
				return fmt.Errorf("%s: bad pattern %q", at, p)
			}
			if !re.MatchString(x) {
				return fmt.Errorf("%s: %q does not match %s", at, x, p)
			}
		}
		if schema["format"] == "date-time" {
			if _, err := time.Parse(time.RFC3339Nano, x); err != nil {
				return fmt.Errorf("%s: %q is not an RFC 3339 time", at, x)
			}
		}
	case json.Number:
		f, _ := x.Float64()
		if m, ok := number(schema["minimum"]); ok && f < m {
			return fmt.Errorf("%s: %v is below %v", at, f, m)
		}
		if m, ok := number(schema["maximum"]); ok && f > m {
			return fmt.Errorf("%s: %v is above %v", at, f, m)
		}
	}
	return nil
}

func (s *Spec) checkObject(schema map[string]any, x map[string]any, at string) error {
	props, _ := schema["properties"].(map[string]any)
	for _, r := range asList(schema["required"]) {
		if _, ok := x[r]; !ok {
			return fmt.Errorf("%s: missing %s", at, r)
		}
	}
	keys := make([]string, 0, len(x))
	for k := range x {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if p, ok := props[k].(map[string]any); ok {
			if err := s.check(p, x[k], at+"."+k); err != nil {
				return err
			}
			continue
		}
		switch ap := schema["additionalProperties"].(type) {
		case bool:
			if !ap {
				return fmt.Errorf("%s: %s is not described", at, k)
			}
		case map[string]any:
			if err := s.check(ap, x[k], at+"."+k); err != nil {
				return err
			}
		}
	}
	return nil
}

func typeMatches(t any, v any) bool {
	for _, name := range asList(t) {
		switch x := v.(type) {
		case nil:
			if name == "null" {
				return true
			}
		case bool:
			if name == "boolean" {
				return true
			}
		case string:
			if name == "string" {
				return true
			}
		case json.Number:
			if name == "number" {
				return true
			}
			if name == "integer" && !strings.ContainsAny(x.String(), ".eE") {
				return true
			}
		case []any:
			if name == "array" {
				return true
			}
		case map[string]any:
			if name == "object" {
				return true
			}
		}
	}
	return false
}

func asList(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

func describe(v any) string {
	if v == nil {
		return "null"
	}
	b, _ := json.Marshal(v)
	if len(b) > 60 {
		b = append(b[:57], "..."...)
	}
	return string(b)
}
