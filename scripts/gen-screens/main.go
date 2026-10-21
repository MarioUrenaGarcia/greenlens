// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Command gen-screens checks the demo of the site in a browser and takes
// the pictures of the interface that the README and the site show. It
// serves a demo written by gen-site-demo, opens every screen in English and
// Spanish and in the dark and the light theme, and fails on errors of the
// page, failed requests and the accessibility problems that the end-to-end
// tests look for. Unless -check is set, it then writes the chosen screens
// as WebP. Every value on them is simulated. Run it with "make screens".
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/webtest/cdp"
)

// screen is a route of the interface and the themes it is pictured in.
type screen struct {
	name, route string
	shots       []string
}

var screens = []screen{
	{"summary", "summary", []string{"dark", "light"}},
	{"models", "models", []string{"dark"}},
	{"model", "models/ollama-11434/llama3.2:3b", []string{"dark"}},
	{"backends", "backends", []string{"dark"}},
	{"consumers", "consumers", nil},
	{"reports", "reports", []string{"light"}},
	{"benchmarks", "benchmarks", nil},
	{"sources", "sources", nil},
	{"help", "help", nil},
	{"about", "about", nil},
}

var (
	langs  = []string{"en", "es"}
	themes = []string{"dark", "light"}
	// namedRoles are the roles whose elements need an accessible name.
	namedRoles = []string{"button", "link", "textbox", "combobox", "checkbox", "radio", "switch", "slider",
		"spinbutton", "searchbox", "listbox", "menuitem", "tab", "image", "img", "progressbar"}
)

const width, height = 1440, 900

type finding struct {
	Kind  string  `json:"kind"`
	Text  string  `json:"text"`
	Ratio float64 `json:"ratio"`
	Need  float64 `json:"need"`
	FG    string  `json:"fg"`
	BG    string  `json:"bg"`
}

// session drives one page of the browser and keeps the errors it reports.
type session struct {
	ctx  context.Context
	p    *cdp.Page
	base string
	mu   sync.Mutex
	errs []string
}

func (s *session) failed(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errs = append(s.errs, msg)
}

// watch records exceptions, console errors and answers with an error
// status until the context ends.
func (s *session) watch() {
	exc, stop1 := s.p.Subscribe("Runtime.exceptionThrown", 64)
	logs, stop2 := s.p.Subscribe("Runtime.consoleAPICalled", 64)
	resp, stop3 := s.p.Subscribe("Network.responseReceived", 256)
	go func() {
		defer stop1()
		defer stop2()
		defer stop3()
		for {
			select {
			case <-s.ctx.Done():
				return
			case e := <-exc:
				var ev struct {
					ExceptionDetails struct {
						Text      string `json:"text"`
						Exception struct {
							Description string `json:"description"`
						} `json:"exception"`
					} `json:"exceptionDetails"`
				}
				_ = json.Unmarshal(e.Params, &ev)
				s.failed("exception: " + ev.ExceptionDetails.Text + " " + ev.ExceptionDetails.Exception.Description)
			case e := <-logs:
				var ev struct {
					Type string                `json:"type"`
					Args []struct{ Value any } `json:"args"`
				}
				_ = json.Unmarshal(e.Params, &ev)
				if ev.Type == "error" {
					s.failed(fmt.Sprint("console error: ", ev.Args))
				}
			case e := <-resp:
				var ev struct {
					Response struct {
						URL    string `json:"url"`
						Status int    `json:"status"`
					} `json:"response"`
				}
				_ = json.Unmarshal(e.Params, &ev)
				if ev.Response.Status >= 400 {
					s.failed(fmt.Sprintf("%d %s", ev.Response.Status, ev.Response.URL))
				}
			}
		}
	}()
}

// open loads the demo with a language and a theme chosen, as the
// preferences of the interface keep them.
func (s *session) open(lang, theme string) error {
	if err := s.p.Navigate(s.ctx, s.base); err != nil {
		return err
	}
	set := fmt.Sprintf(`localStorage.setItem("greenlens.lang", %q); localStorage.setItem("greenlens.theme", %q); localStorage.setItem("greenlens.welcome", "done")`, lang, theme)
	if err := s.p.Eval(s.ctx, set, nil); err != nil {
		return err
	}
	media := map[string]any{"features": []map[string]string{{"name": "prefers-color-scheme", "value": theme}}}
	if err := s.p.Call(s.ctx, "Emulation.setEmulatedMedia", media, nil); err != nil {
		return err
	}
	if err := s.p.Reload(s.ctx); err != nil {
		return err
	}
	return s.wait(`document.querySelector("nav.sidebar")`, 15*time.Second)
}

func (s *session) wait(expr string, d time.Duration) error {
	ctx, cancel := context.WithTimeout(s.ctx, d)
	defer cancel()
	return s.p.WaitFor(ctx, expr)
}

// show opens a screen and waits until its data and fonts have loaded and,
// for settle, until its charts have drawn.
func (s *session) show(route string, settle time.Duration) error {
	if err := s.p.Eval(s.ctx, fmt.Sprintf(`location.hash = "#/%s"`, route), nil); err != nil {
		return err
	}
	if err := s.wait(`document.querySelector("main h1") && !document.querySelector("main p.muted[role=status]")`, 15*time.Second); err != nil {
		return fmt.Errorf("%s: %w", route, err)
	}
	if err := s.p.Eval(s.ctx, `document.fonts.ready.then(() => true)`, nil); err != nil {
		return err
	}
	time.Sleep(settle)
	return nil
}

// problems checks the page as it is now: structure, contrast and the
// names of every control in the accessibility tree.
func (s *session) problems(lang string) ([]string, error) {
	var found []finding
	if err := s.p.Eval(s.ctx, fmt.Sprintf(`[...window.__glAudit.structure(%q), ...window.__glAudit.contrast()]`, lang), &found); err != nil {
		return nil, err
	}
	var out []string
	for _, f := range found {
		if f.Kind == "contrast" {
			out = append(out, fmt.Sprintf("contrast %q ratio %.2f < %.1f (fg %s, bg %s)", f.Text, f.Ratio, f.Need, f.FG, f.BG))
			continue
		}
		out = append(out, fmt.Sprintf("%s %q", f.Kind, f.Text))
	}
	nodes, err := s.p.AXTree(s.ctx)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		role, name := n.RoleName()
		if !n.Ignored && slices.Contains(namedRoles, strings.ToLower(role)) && strings.TrimSpace(name) == "" {
			out = append(out, fmt.Sprintf("%s without an accessible name (DOM node %d)", role, n.BackendDOMNodeID))
		}
	}
	return out, nil
}

func (s *session) capture(path string) error {
	var shot struct {
		Data string `json:"data"`
	}
	if err := s.p.Call(s.ctx, "Page.captureScreenshot", map[string]any{"format": "webp", "quality": 82}, &shot); err != nil {
		return err
	}
	b, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // pictures of the site
		return err
	}
	return os.WriteFile(path, b, 0o644) //nolint:gosec // public picture of the site
}

// serve answers the files of the demo on a free port of the loopback.
func serve(ctx context.Context, dir string) (string, func(), error) {
	l, err := new(net.ListenConfig).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	srv := &http.Server{Handler: http.FileServer(http.Dir(dir)), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "gen-screens:", err)
		}
	}()
	return "http://" + l.Addr().String() + "/", func() { _ = srv.Close() }, nil
}

func run(demo, out, audit string, check bool) error {
	if _, err := os.Stat(filepath.Join(demo, "demo.js")); err != nil {
		return fmt.Errorf("no demo in %s; run go run ./scripts/gen-site-demo first", demo)
	}
	script, err := os.ReadFile(audit) //nolint:gosec // a file of the repository named on the command line
	if err != nil {
		return err
	}
	chrome, err := cdp.Find()
	if err != nil {
		// As the end-to-end tests do, the check needs a browser only where
		// GREENLENS_CHROME names one, as in CI.
		if check && os.Getenv("GREENLENS_CHROME") == "" {
			fmt.Println("demo check skipped:", err)
			return nil
		}
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	base, stop, err := serve(ctx, demo)
	if err != nil {
		return err
	}
	defer stop()
	profile, err := os.MkdirTemp("", "gen-screens-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(profile) }()
	b, err := cdp.Launch(chrome, profile, fmt.Sprintf("--window-size=%d,%d", width, height), "--hide-scrollbars", "--lang=en-US")
	if err != nil {
		return err
	}
	defer func() { _ = b.Close() }()
	p, err := b.NewPage(ctx)
	if err != nil {
		return err
	}
	if err := p.Call(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": string(script)}, nil); err != nil {
		return err
	}
	metrics := map[string]any{"width": width, "height": height, "deviceScaleFactor": 1, "mobile": false}
	if err := p.Call(ctx, "Emulation.setDeviceMetricsOverride", metrics, nil); err != nil {
		return err
	}
	s := &session{ctx: ctx, p: p, base: base}
	s.watch()
	// Pictures wait for the charts to draw; the checks only need the page.
	settle := 2500 * time.Millisecond
	if check {
		settle = 300 * time.Millisecond
	}
	var problems []string
	wrote := 0
	for _, lang := range langs {
		for _, theme := range themes {
			if err := s.open(lang, theme); err != nil {
				return fmt.Errorf("%s %s: %w", lang, theme, err)
			}
			for _, sc := range screens {
				if err := s.show(sc.route, settle); err != nil {
					return fmt.Errorf("%s %s: %w", lang, theme, err)
				}
				found, err := s.problems(lang)
				if err != nil {
					return err
				}
				for _, f := range found {
					problems = append(problems, fmt.Sprintf("%s %s %s: %s", lang, theme, sc.name, f))
				}
				if check || !slices.Contains(sc.shots, theme) {
					continue
				}
				if err := s.capture(filepath.Join(out, fmt.Sprintf("%s-%s-%s.webp", sc.name, theme, lang))); err != nil {
					return err
				}
				wrote++
			}
		}
	}
	s.mu.Lock()
	problems = append(problems, s.errs...)
	s.mu.Unlock()
	if len(problems) > 0 {
		return fmt.Errorf("the demo has problems:\n  %s", strings.Join(problems, "\n  "))
	}
	if check {
		fmt.Printf("demo checked: %d screens in %d languages and %d themes\n", len(screens), len(langs), len(themes))
	} else {
		fmt.Printf("wrote %d pictures to %s\n", wrote, out)
	}
	return nil
}

func main() {
	demo := flag.String("demo", filepath.Join("site", "static", "demo"), "directory of the demo")
	out := flag.String("out", filepath.Join("site", "static", "images", "screens"), "directory for the pictures")
	audit := flag.String("audit", filepath.Join("internal", "agent", "testdata", "audit.js"), "accessibility checks to run in the page")
	check := flag.Bool("check", false, "check the demo without writing pictures")
	flag.Parse()
	if err := run(*demo, *out, *audit, *check); err != nil {
		fmt.Fprintln(os.Stderr, "gen-screens:", err)
		os.Exit(1)
	}
}
