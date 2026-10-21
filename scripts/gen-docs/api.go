// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MarioUrenaGarcia/greenlens/internal/apispec"
)

type apiTexts struct {
	title, intro, operations, documents         string
	method, path, role, summary                 string
	parameters, name, in, typ, required, desc   string
	responses, status, document, field, yes, no string
	listOf, mapOf, oneOf                        string
}

var apiPages = map[string]apiTexts{
	"en": {
		title:      "HTTP API",
		intro:      "Every operation of the HTTP API of the agent, generated from api/openapi.yaml, which the agent also serves at /api/v1/openapi.yaml. Requests need a session or an access token unless the role is public; the guide to the HTTP API explains roles and tokens.",
		operations: "Operations", documents: "Documents",
		method: "Method", path: "Path", role: "Role", summary: "Summary",
		parameters: "Parameters", name: "Name", in: "In", typ: "Type", required: "Required", desc: "Description",
		responses: "Responses", status: "Status", document: "Document", field: "Field", yes: "yes", no: "no",
		listOf: "list of %s", mapOf: "map of %s", oneOf: "%s or %s",
	},
	"es": {
		title:      "API HTTP",
		intro:      "Todas las operaciones de la API HTTP del agente, generadas desde api/openapi.yaml, que el agente también sirve en /api/v1/openapi.yaml. Las solicitudes necesitan una sesión o un token de acceso salvo que el rol sea public; la guía de la API HTTP explica los roles y los tokens.",
		operations: "Operaciones", documents: "Documentos",
		method: "Método", path: "Ruta", role: "Rol", summary: "Resumen",
		parameters: "Parámetros", name: "Nombre", in: "En", typ: "Tipo", required: "Obligatorio", desc: "Descripción",
		responses: "Respuestas", status: "Estado", document: "Documento", field: "Campo", yes: "sí", no: "no",
		listOf: "lista de %s", mapOf: "mapa de %s", oneOf: "%s o %s",
	},
}

// text returns the description of a node in a language.
func text(m map[string]any, key, lang string) string {
	if lang != "en" {
		if s, ok := m["x-"+key+"-"+lang].(string); ok {
			return strings.Join(strings.Fields(s), " ")
		}
	}
	s, _ := m[key].(string)
	return strings.Join(strings.Fields(s), " ")
}

func apiPage(spec *apispec.Spec, lang string) string {
	p := apiPages[lang]
	var b strings.Builder
	b.WriteString(frontMatter(p.title, 25))
	b.WriteString(p.intro + "\n\n")
	if info, ok := spec.Doc["info"].(map[string]any); ok {
		b.WriteString(text(info, "description", lang) + "\n\n")
	}
	ops := spec.Operations()
	fmt.Fprintf(&b, "## %s\n\n| %s | %s | %s | %s |\n|---|---|---|---|\n", p.operations, p.method, p.path, p.role, p.summary)
	for _, op := range ops {
		fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s |\n", op.Method, op.Path, op.Role, escape(text(op.Raw, "summary", lang)))
	}
	for _, op := range ops {
		fmt.Fprintf(&b, "\n### %s %s\n\n", op.Method, op.Path)
		b.WriteString(text(op.Raw, "summary", lang) + ".")
		if d := text(op.Raw, "description", lang); d != "" {
			b.WriteString(" " + d)
		}
		fmt.Fprintf(&b, "\n\n%s: `%s`\n", p.role, op.Role)
		if params, ok := op.Raw["parameters"].([]any); ok && len(params) > 0 {
			fmt.Fprintf(&b, "\n**%s**\n\n| %s | %s | %s | %s | %s |\n|---|---|---|---|---|\n", p.parameters, p.name, p.in, p.typ, p.required, p.desc)
			for _, x := range params {
				pm, _ := x.(map[string]any)
				schema, _ := pm["schema"].(map[string]any)
				req := p.no
				if r, _ := pm["required"].(bool); r {
					req = p.yes
				}
				fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n", pm["name"], pm["in"], typeText(p, schema), req, escape(text(pm, "description", lang)))
			}
		}
		if body, ok := op.Raw["requestBody"].(map[string]any); ok {
			if schema := jsonSchema(body); schema != nil {
				fmt.Fprintf(&b, "\n**%s**: %s\n", p.document, typeText(p, schema))
			}
		}
		rs, _ := op.Raw["responses"].(map[string]any)
		codes := make([]string, 0, len(rs))
		for c := range rs {
			codes = append(codes, c)
		}
		slices.Sort(codes)
		fmt.Fprintf(&b, "\n**%s**\n\n| %s | %s | %s |\n|---|---|---|\n", p.responses, p.status, p.desc, p.document)
		for _, c := range codes {
			r := response(spec, rs[c])
			doc := ""
			if schema := jsonSchema(r); schema != nil {
				doc = typeText(p, schema)
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", c, escape(text(r, "description", lang)), doc)
		}
	}
	fmt.Fprintf(&b, "\n## %s\n", p.documents)
	schemas := spec.Schemas()
	names := make([]string, 0, len(schemas))
	for n := range schemas {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		s, _ := schemas[n].(map[string]any)
		fmt.Fprintf(&b, "\n### %s\n\n%s\n", n, text(s, "description", lang))
		props, _ := s["properties"].(map[string]any)
		if len(props) == 0 {
			if s["type"] != "object" {
				fmt.Fprintf(&b, "\n%s: %s\n", p.typ, typeText(p, s))
			}
			continue
		}
		req := asStrings(s["required"])
		fields := make([]string, 0, len(props))
		for f := range props {
			fields = append(fields, f)
		}
		// Required fields first, each group in alphabetical order.
		slices.SortFunc(fields, func(x, y string) int {
			rx, ry := slices.Contains(req, x), slices.Contains(req, y)
			if rx != ry {
				if rx {
					return -1
				}
				return 1
			}
			return strings.Compare(x, y)
		})
		fmt.Fprintf(&b, "\n| %s | %s | %s | %s |\n|---|---|---|---|\n", p.field, p.typ, p.required, p.desc)
		for _, f := range fields {
			fs, _ := props[f].(map[string]any)
			r := p.no
			if slices.Contains(req, f) {
				r = p.yes
			}
			d := text(fs, "description", lang)
			if d == "" {
				if ref, ok := fs["$ref"].(string); ok {
					target, _ := schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
					d = text(target, "description", lang)
				}
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", f, typeText(p, fs), r, escape(d))
		}
	}
	return b.String()
}

func response(spec *apispec.Spec, v any) map[string]any {
	r, _ := v.(map[string]any)
	if ref, ok := r["$ref"].(string); ok {
		comp, _ := spec.Doc["components"].(map[string]any)
		all, _ := comp["responses"].(map[string]any)
		r, _ = all[strings.TrimPrefix(ref, "#/components/responses/")].(map[string]any)
	}
	return r
}

func jsonSchema(m map[string]any) map[string]any {
	content, _ := m["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	schema, _ := media["schema"].(map[string]any)
	return schema
}

// typeText names the type of a schema, linking named documents.
func typeText(p apiTexts, s map[string]any) string {
	if ref, ok := s["$ref"].(string); ok {
		n := strings.TrimPrefix(ref, "#/components/schemas/")
		return "[" + n + "](#" + strings.ToLower(n) + ")"
	}
	if alts, ok := s["oneOf"].([]any); ok && len(alts) == 2 {
		a, _ := alts[0].(map[string]any)
		c, _ := alts[1].(map[string]any)
		return fmt.Sprintf(p.oneOf, typeText(p, a), typeText(p, c))
	}
	t, _ := s["type"].(string)
	switch t {
	case "array":
		items, _ := s["items"].(map[string]any)
		return fmt.Sprintf(p.listOf, typeText(p, items))
	case "object":
		if ap, ok := s["additionalProperties"].(map[string]any); ok {
			return fmt.Sprintf(p.mapOf, typeText(p, ap))
		}
		return "object"
	case "string":
		if f, ok := s["format"].(string); ok {
			return "string (" + f + ")"
		}
		if e := asStrings(s["enum"]); len(e) > 0 {
			return "`" + strings.Join(e, "`, `") + "`"
		}
	}
	return t
}

func asStrings(v any) []string {
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
