// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package helpdocs serves the documentation embedded in the binary to the
// graphical interface: an index of pages by section, each page rendered to
// HTML, and a search. Raw HTML in the sources is dropped by the renderer,
// and links between pages become routes of the interface, so the help
// works without the documentation site or internet.
package helpdocs

import (
	"bytes"
	"io/fs"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"go.yaml.in/yaml/v3"
)

// Sections of the documentation, in the order of the site.
var Sections = []string{"tutorials", "guides", "reference", "explanation"}

// Page describes one page.
type Page struct {
	Path        string `json:"path"`
	Section     string `json:"section"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Weight      int    `json:"-"`
	source      []byte
	plain       string
}

// Result is a page that matches a search.
type Result struct {
	Path    string `json:"path"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}

// Docs holds the pages of every language.
type Docs struct {
	langs    map[string]map[string]*Page
	md       goldmark.Markdown
	mu       sync.Mutex
	rendered map[string]string
}

// Load reads the documentation: English under the root of fsys and every
// other language under a directory named after it, such as es/.
func Load(fsys fs.FS, others []string) (*Docs, error) {
	d := &Docs{
		langs:    map[string]map[string]*Page{},
		rendered: map[string]string{},
		md: goldmark.New(
			goldmark.WithExtensions(extension.GFM),
			goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		),
	}
	err := fs.WalkDir(fsys, ".", func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || path.Ext(p) != ".md" {
			return err
		}
		lang, rel := "en", p
		if first, rest, ok := strings.Cut(p, "/"); ok && slices.Contains(others, first) {
			lang, rel = first, rest
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		page := parsePage(rel, data)
		if d.langs[lang] == nil {
			d.langs[lang] = map[string]*Page{}
		}
		d.langs[lang][page.Path] = page
		return nil
	})
	return d, err
}

func parsePage(rel string, data []byte) *Page {
	name := strings.TrimSuffix(rel, ".md")
	if path.Base(name) == "_index" {
		name = path.Dir(name)
		if name == "." {
			name = ""
		}
	}
	p := &Page{Path: name, source: data}
	if section, _, _ := strings.Cut(name, "/"); slices.Contains(Sections, section) {
		p.Section = section
	}
	if rest, ok := bytes.CutPrefix(data, []byte("---\n")); ok {
		if fm, body, ok := bytes.Cut(rest, []byte("\n---\n")); ok {
			var meta struct {
				Title       string `yaml:"title"`
				Description string `yaml:"description"`
				Weight      int    `yaml:"weight"`
			}
			if yaml.Unmarshal(fm, &meta) == nil {
				p.Title, p.Description, p.Weight = meta.Title, meta.Description, meta.Weight
			}
			p.source = body
		}
	}
	if p.Title == "" {
		p.Title = name
	}
	p.plain = plainText(p.source)
	return p
}

// plainText strips the Markdown marks that matter for searching.
func plainText(src []byte) string {
	var b strings.Builder
	for _, r := range string(src) {
		if strings.ContainsRune("#*`|>[]()_", r) {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func (d *Docs) pages(lang string) map[string]*Page {
	if p, ok := d.langs[lang]; ok {
		return p
	}
	return d.langs["en"]
}

// page returns a page in a language, or in English when it has no
// translation.
func (d *Docs) page(lang, p string) (*Page, string, bool) {
	if pg, ok := d.pages(lang)[p]; ok {
		return pg, lang, true
	}
	pg, ok := d.langs["en"][p]
	return pg, "en", ok
}

// Index lists the pages of a language by section and weight.
func (d *Docs) Index(lang string) []Page {
	var out []Page
	for _, p := range d.pages(lang) {
		out = append(out, Page{Path: p.Path, Section: p.Section, Title: p.Title, Description: p.Description, Weight: p.Weight})
	}
	slices.SortFunc(out, func(a, b Page) int {
		sa, sb := slices.Index(Sections, a.Section), slices.Index(Sections, b.Section)
		switch {
		case sa != sb:
			return sa - sb
		case (a.Path == a.Section) != (b.Path == b.Section):
			// The page of a section comes before its pages.
			if a.Path == a.Section {
				return -1
			}
			return 1
		case a.Weight != b.Weight:
			return a.Weight - b.Weight
		}
		return strings.Compare(a.Title, b.Title)
	})
	return out
}

// Render returns the title and the HTML of a page.
func (d *Docs) Render(lang, p string) (string, string, bool) {
	page, found, ok := d.page(lang, p)
	if !ok {
		return "", "", false
	}
	key := found + "\x00" + p
	d.mu.Lock()
	html, done := d.rendered[key]
	d.mu.Unlock()
	if done {
		return page.Title, html, true
	}
	doc := d.md.Parser().Parse(text.NewReader(page.source))
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if l, ok := n.(*ast.Link); ok && entering {
			dest, external := d.rewrite(found, p, string(l.Destination))
			l.Destination = []byte(dest)
			if external {
				l.SetAttributeString("rel", "noreferrer")
			}
		}
		return ast.WalkContinue, nil
	})
	var b bytes.Buffer
	if err := d.md.Renderer().Render(&b, page.source, doc); err != nil {
		return "", "", false
	}
	html = b.String()
	d.mu.Lock()
	d.rendered[key] = html
	d.mu.Unlock()
	return page.Title, html, true
}

// rewrite turns a link of the documentation into a route of the
// interface. Links between pages are written for the pretty URLs of the
// site, where a page lives in a directory of its own.
func (d *Docs) rewrite(lang, current, dest string) (string, bool) {
	u, err := url.Parse(dest)
	if err != nil {
		return "#/help/" + current, false
	}
	if u.Scheme != "" || u.Host != "" {
		return dest, true
	}
	target := current
	if u.Path != "" {
		base := &url.URL{Path: "/" + current + "/"}
		target = strings.Trim(base.ResolveReference(&url.URL{Path: u.Path}).Path, "/")
		target = strings.TrimSuffix(target, ".md")
		target = strings.TrimPrefix(target, "docs/")
		if lang != "en" {
			target = strings.TrimPrefix(target, lang+"/")
		}
	}
	if _, _, ok := d.page(lang, target); !ok {
		target = current
	}
	r := "#/help/" + target
	if u.Fragment != "" {
		r += "?anchor=" + url.QueryEscape(u.Fragment)
	}
	return r, false
}

// Search finds the pages of a language that contain every word of q, the
// best first: words in the title count more than words in the text.
func (d *Docs) Search(lang, q string) []Result {
	words := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(words) == 0 {
		return []Result{}
	}
	type hit struct {
		r     Result
		score int
	}
	var hits []hit
	for _, p := range d.pages(lang) {
		title, body := strings.ToLower(p.Title), strings.ToLower(p.plain)
		score := 0
		for _, w := range words {
			n := strings.Count(body, w)
			t := strings.Count(title, w)
			if n == 0 && t == 0 {
				score = 0
				break
			}
			score += n + 10*t
		}
		if score > 0 {
			hits = append(hits, hit{Result{Path: p.Path, Title: p.Title, Snippet: snippet(p.plain, words[0])}, score})
		}
	}
	slices.SortFunc(hits, func(a, b hit) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return strings.Compare(a.r.Title, b.r.Title)
	})
	out := make([]Result, 0, min(len(hits), 20))
	for _, h := range hits[:min(len(hits), 20)] {
		out = append(out, h.r)
	}
	return out
}

// snippet returns the text around the first match of word.
func snippet(plain, word string) string {
	runes := []rune(plain)
	lower := []rune(strings.ToLower(plain))
	w := []rune(word)
	at := -1
	for i := 0; i+len(w) <= len(lower); i++ {
		if string(lower[i:i+len(w)]) == word {
			at = i
			break
		}
	}
	if at < 0 {
		at = 0
	}
	start, end := max(0, at-60), min(len(runes), at+120)
	s := strings.TrimSpace(string(runes[start:end]))
	if start > 0 {
		s = "..." + s
	}
	if end < len(runes) {
		s += "..."
	}
	return s
}
