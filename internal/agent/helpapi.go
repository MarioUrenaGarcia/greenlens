// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/MarioUrenaGarcia/greenlens"
	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/helpdocs"
	"github.com/MarioUrenaGarcia/greenlens/internal/i18n"
)

// The documentation is read once, the first time help is asked for.
var docs = sync.OnceValues(func() (*helpdocs.Docs, error) {
	var others []string
	for _, l := range i18n.Supported {
		if l != i18n.Default {
			others = append(others, l)
		}
	}
	return helpdocs.Load(greenlens.Docs(), others)
})

func helpLang(r *http.Request) string {
	if l := r.URL.Query().Get("lang"); slices.Contains(i18n.Supported, l) {
		return l
	}
	return i18n.Default
}

func (a *Agent) handleHelp(w http.ResponseWriter, r *http.Request) {
	d, err := docs()
	if err != nil {
		a.log.Error("the documentation cannot be read", "error", err)
		writeError(w, http.StatusInternalServerError, "the documentation cannot be read")
		return
	}
	lang := helpLang(r)
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		if len(q) > 200 {
			writeError(w, http.StatusBadRequest, "the search is longer than 200 characters")
			return
		}
		writeJSON(w, http.StatusOK, api.HelpSearch{SchemaVersion: api.SchemaVersion, Lang: lang, Query: q, Results: d.Search(lang, q)})
		return
	}
	writeJSON(w, http.StatusOK, api.HelpIndex{SchemaVersion: api.SchemaVersion, Lang: lang, Sections: helpdocs.Sections, Pages: d.Index(lang)})
}

func (a *Agent) handleHelpPage(w http.ResponseWriter, r *http.Request) {
	d, err := docs()
	if err != nil {
		a.log.Error("the documentation cannot be read", "error", err)
		writeError(w, http.StatusInternalServerError, "the documentation cannot be read")
		return
	}
	lang := helpLang(r)
	p := strings.Trim(r.PathValue("page"), "/")
	title, html, ok := d.Render(lang, p)
	if !ok {
		writeError(w, http.StatusNotFound, "no help page "+p)
		return
	}
	writeJSON(w, http.StatusOK, api.HelpPage{SchemaVersion: api.SchemaVersion, Lang: lang, Path: p, Title: title, HTML: html})
}
