// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package config

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Values returns every key of cfg written as for its environment variable:
// lists separated by commas, maps as key=value pairs, and an empty text for
// a number that is not set. Lists of objects, such as backends, are left
// out; they are edited only in the file.
func Values(cfg Config) map[string]string {
	out := map[string]string{}
	walk(reflect.ValueOf(&cfg).Elem(), "", func(key string, v reflect.Value) {
		if isObjectList(v.Type()) {
			return
		}
		out[key] = valueText(v)
	})
	return out
}

func valueText(v reflect.Value) string {
	switch {
	case v.Type() == durationType:
		return time.Duration(v.Int()).String()
	case v.Kind() == reflect.Pointer:
		if v.IsNil() {
			return ""
		}
		return strconv.FormatFloat(v.Elem().Float(), 'g', -1, 64)
	case v.Kind() == reflect.Slice:
		parts := make([]string, v.Len())
		for i := range parts {
			parts[i] = v.Index(i).String()
		}
		return strings.Join(parts, ",")
	case v.Kind() == reflect.Map:
		var parts []string
		for _, k := range v.MapKeys() {
			parts = append(parts, k.String()+"="+v.MapIndex(k).String())
		}
		slices.Sort(parts)
		return strings.Join(parts, ",")
	case v.Kind() == reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'g', -1, 64)
	}
	return fmt.Sprint(v.Interface())
}

// Edit sets keys of a YAML configuration document and returns the new
// document. Comments and the order of keys are kept; values are written as
// for their environment variable, as Values returns them. The result is
// not validated: parse it and call Validate.
func Edit(data []byte, changes map[string]string) ([]byte, error) {
	doc, err := parseDoc(data)
	if err != nil {
		return nil, err
	}
	types := map[string]string{}
	for _, f := range Fields() {
		if !strings.Contains(f.Key, "[]") && f.Type != "list of objects" {
			types[f.Key] = f.Type
		}
	}
	keys := make([]string, 0, len(changes))
	for k := range changes {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var errs []error
	for _, key := range keys {
		typ, ok := types[key]
		if !ok {
			errs = append(errs, fmt.Errorf("%s: unknown key", key))
			continue
		}
		node, err := valueNode(typ, changes[key])
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
			continue
		}
		setNode(doc.Content[0], strings.Split(key, "."), node)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return encodeDoc(doc)
}

func parseDoc(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if len(bytes.TrimSpace(data)) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("the configuration must be a YAML mapping")
	}
	return &doc, nil
}

func encodeDoc(doc *yaml.Node) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// SetAlertRules replaces alerts.rules in a YAML configuration document,
// keeping the comments and the order of every other key. The result is not
// validated: parse it and call Validate.
func SetAlertRules(data []byte, rules []AlertRule) ([]byte, error) {
	doc, err := parseDoc(data)
	if err != nil {
		return nil, err
	}
	str := func(v string) *yaml.Node {
		// Operators such as ">" would start a block scalar unquoted.
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v, Style: yaml.DoubleQuotedStyle}
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	for _, r := range rules {
		m := &yaml.Node{Kind: yaml.MappingNode}
		add := func(k string, v *yaml.Node) { m.Content = append(m.Content, scalar("!!str", k), v) }
		add("name", str(r.Name))
		add("expr", str(r.Expr))
		add("op", str(r.Op))
		// Without a tag the number is written plainly and read back as one.
		add("threshold", &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.FormatFloat(r.Threshold, 'g', -1, 64)})
		add("for", str(ShortDuration(r.For.D())))
		if len(r.Match) > 0 {
			mm := &yaml.Node{Kind: yaml.MappingNode}
			for _, k := range slices.Sorted(maps.Keys(r.Match)) {
				mm.Content = append(mm.Content, scalar("!!str", k), str(r.Match[k]))
			}
			add("match", mm)
		}
		seq.Content = append(seq.Content, m)
	}
	if len(rules) == 0 {
		seq.Style = yaml.FlowStyle
	}
	setNode(doc.Content[0], []string{"alerts", "rules"}, seq)
	return encodeDoc(doc)
}

func scalar(tag, value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
}

func valueNode(typ, s string) (*yaml.Node, error) {
	s = strings.TrimSpace(s)
	switch typ {
	case "bool":
		b, err := strconv.ParseBool(s)
		if err != nil {
			return nil, errors.New("must be true or false")
		}
		return scalar("!!bool", strconv.FormatBool(b)), nil
	case "integer":
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, errors.New("must be an integer")
		}
		return scalar("!!int", strconv.Itoa(n)), nil
	case "number":
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, errors.New("must be a number")
		}
		return scalar("!!float", strconv.FormatFloat(f, 'g', -1, 64)), nil
	case "number or null":
		if s == "" || s == "null" {
			return scalar("!!null", "null"), nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, errors.New("must be a number, or empty for none")
		}
		return scalar("!!float", strconv.FormatFloat(f, 'g', -1, 64)), nil
	case "duration":
		if _, err := time.ParseDuration(s); err != nil {
			return nil, errors.New("must be a duration such as 1s, 10m or 24h")
		}
		return scalar("!!str", s), nil
	case "list of strings":
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				seq.Content = append(seq.Content, scalar("!!str", p))
			}
		}
		return seq, nil
	case "map of strings":
		m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle}
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p == "" {
				continue
			}
			k, v, ok := strings.Cut(p, "=")
			if !ok {
				return nil, errors.New("must be key=value pairs separated by commas")
			}
			m.Content = append(m.Content, scalar("!!str", strings.TrimSpace(k)), scalar("!!str", strings.TrimSpace(v)))
		}
		return m, nil
	}
	n := scalar("!!str", s)
	// An empty or ambiguous text keeps its quotes, so it stays a string.
	if s == "" || s != strings.TrimSpace(s) || strings.ContainsAny(s, ":#{}[],&*!|>'\"%@`") {
		n.Style = yaml.DoubleQuotedStyle
	}
	return n, nil
}

// setNode puts value at the path of keys inside a mapping, creating the
// mappings on the way.
func setNode(m *yaml.Node, path []string, value *yaml.Node) {
	for n, key := range path {
		last := n == len(path)-1
		var next *yaml.Node
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value != key {
				continue
			}
			if last {
				// The comments of the old value belong to the line.
				value.LineComment, value.HeadComment = m.Content[i+1].LineComment, m.Content[i+1].HeadComment
				m.Content[i+1] = value
				return
			}
			if m.Content[i+1].Kind != yaml.MappingNode {
				m.Content[i+1] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			}
			next = m.Content[i+1]
			break
		}
		if next == nil {
			if last {
				m.Content = append(m.Content, scalar("!!str", key), value)
				return
			}
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			m.Content = append(m.Content, scalar("!!str", key), next)
		}
		m = next
	}
}

// ShortDuration writes a duration without its empty trailing units, as
// people write it: 5m rather than 5m0s, 1h30m rather than 1h30m0s.
func ShortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
