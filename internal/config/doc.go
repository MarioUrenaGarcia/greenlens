// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package config

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/attribution"
)

// Field describes one configuration key for the reference documentation
// and the JSON schema.
type Field struct {
	Key     string
	Env     string
	Type    string
	Default string
	Enum    []string
}

// DocKey is the catalog key that holds the description of a field.
func (f Field) DocKey() string { return "config.doc." + strings.ReplaceAll(f.Key, ".", "_") }

// enums lists the accepted values of string fields that have a fixed set.
func enums() map[string][]string {
	var policies []string
	for _, p := range attribution.Policies {
		policies = append(policies, string(p))
	}
	return map[string][]string{
		"attribution.idle_policy": policies,
		"logging.level":           {"debug", "info", "warn", "error"},
		"logging.format":          {"json", "text"},
		"sources.disable":         SourceIDs,
	}
}

// Fields lists every key with its type and default value.
func Fields() []Field {
	def := Defaults()
	var out []Field
	walk(reflect.ValueOf(&def).Elem(), "", func(key string, v reflect.Value) {
		out = append(out, Field{Key: key, Env: envName(key), Type: typeName(v.Type()), Default: defaultText(v), Enum: enums()[key]})
	})
	return out
}

func typeName(t reflect.Type) string {
	switch {
	case t == durationType:
		return "duration"
	case t.Kind() == reflect.Pointer:
		return "number or null"
	case t.Kind() == reflect.Slice:
		return "list of strings"
	case t.Kind() == reflect.Map:
		return "map of strings"
	case t.Kind() == reflect.Float64:
		return "number"
	default:
		return t.Kind().String()
	}
}

func defaultText(v reflect.Value) string {
	switch {
	case v.Type() == durationType:
		return time.Duration(v.Int()).String()
	case v.Kind() == reflect.Pointer:
		if v.IsNil() {
			return "null"
		}
		return fmt.Sprint(v.Elem().Interface())
	case v.Kind() == reflect.Slice || v.Kind() == reflect.Map:
		if v.Len() == 0 {
			return "empty"
		}
	case v.Kind() == reflect.String && v.String() == "":
		return "empty"
	}
	return fmt.Sprint(v.Interface())
}

// Schema returns the JSON schema of the configuration file. Unknown keys
// are rejected at every level, as the loader does.
func Schema(describe func(Field) string) map[string]any {
	root := object()
	for _, f := range Fields() {
		node := root
		parts := strings.Split(f.Key, ".")
		for _, p := range parts[:len(parts)-1] {
			props := node["properties"].(map[string]any)
			child, ok := props[p].(map[string]any)
			if !ok {
				child = object()
				props[p] = child
			}
			node = child
		}
		node["properties"].(map[string]any)[parts[len(parts)-1]] = fieldSchema(f, describe(f))
	}
	root["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	root["$id"] = "https://mariourenagarcia.github.io/greenlens/config.schema.json"
	root["title"] = "GreenLens configuration"
	return root
}

func object() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}}
}

func fieldSchema(f Field, desc string) map[string]any {
	s := map[string]any{"description": desc}
	switch f.Type {
	case "duration":
		s["type"] = "string"
		s["pattern"] = `^([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+$`
	case "number or null":
		s["type"] = []string{"number", "null"}
	case "number":
		s["type"] = "number"
	case "bool":
		s["type"] = "boolean"
	case "list of strings":
		items := map[string]any{"type": "string"}
		if len(f.Enum) > 0 {
			items["enum"] = f.Enum
		}
		s["type"] = "array"
		s["items"] = items
	case "map of strings":
		s["type"] = "object"
		s["additionalProperties"] = map[string]any{"type": "string", "enum": SourceIDs}
		var domains []string
		for _, d := range ForcibleDomains {
			domains = append(domains, string(d))
		}
		s["propertyNames"] = map[string]any{"enum": domains}
	default:
		s["type"] = "string"
		if len(f.Enum) > 0 {
			s["enum"] = slices.Clone(f.Enum)
		}
	}
	return s
}
