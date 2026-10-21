// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

//go:build e2e

package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/webtest/cdp"
)

//go:embed testdata/audit.js
var auditJS string

// Budgets of the interface: what the first visit downloads, and the time
// until the summary shows a measured value.
const (
	loadBudget   = 250 << 10
	renderBudget = time.Second
)

// screenIDs are the screens of the navigation, plus the first steps.
var screenIDs = []string{"summary", "models", "backends", "consumers", "reports", "benchmarks", "sources", "config", "help", "about", "welcome"}

// namedRoles are the roles whose elements need an accessible name.
var namedRoles = []string{"button", "link", "textbox", "combobox", "checkbox", "radio", "switch", "slider", "spinbutton", "searchbox", "listbox", "menuitem", "tab", "image", "img", "progressbar"}

// ollamaServer answers as Ollama does for discovery and benchmarks.
func ollamaServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"version":"0.40.6"}`)
	})
	mux.HandleFunc("GET /api/ps", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"models":[{"name":"qwen2.5:0.5b"}]}`)
	})
	mux.HandleFunc("POST /api/generate", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		time.Sleep(15 * time.Millisecond)
		_, _ = io.WriteString(w, `{"model":"qwen2.5:0.5b","response":"ok","done":true,"prompt_eval_count":12,"eval_count":8}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

type gui struct {
	t    *testing.T
	ctx  context.Context
	p    *cdp.Page
	base string
	sock call
	mu   sync.Mutex
	errs []string
}

// startGUI starts an agent with its HTTP API, sampling every interval,
// and a browser on its interface. Without a browser the test is skipped,
// unless GREENLENS_CHROME names one, as in CI.
func startGUI(t *testing.T, interval time.Duration) (*running, *gui) {
	t.Helper()
	path, err := cdp.Find()
	if err != nil {
		if os.Getenv("GREENLENS_CHROME") != "" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
	up := ollamaServer(t)
	listen := freePort(t)
	r := startAgentWith(t, "", func(c *config.Config) {
		c.Server.Enabled = true
		c.Server.Listen = listen
		c.Backends = []config.Backend{{Name: "ollama-test", Kind: "ollama", Endpoint: up.URL}}
		c.Agent.SampleInterval = config.Duration(interval)
	})
	waitSamples(t, r, 3)
	// The interface follows the languages of the browser; the checks expect
	// English first.
	b, err := cdp.Launch(path, t.TempDir(), "--window-size=1280,900", "--lang=en-US", "--accept-lang=en-US,en")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	p, err := b.NewPage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	g := &gui{t: t, ctx: ctx, p: p, base: "http://" + listen, sock: call{t: t, c: socketClient(r.socket), base: "http://agent"}}
	if err := p.Call(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": auditJS}, nil); err != nil {
		t.Fatal(err)
	}
	if err := p.Call(ctx, "Emulation.setDeviceMetricsOverride", map[string]any{"width": 1280, "height": 900, "deviceScaleFactor": 1, "mobile": false}, nil); err != nil {
		t.Fatal(err)
	}
	// Errors of the page fail the test: uncaught exceptions and
	// console.error.
	go g.collect(p, "Runtime.exceptionThrown")
	go g.collect(p, "Runtime.consoleAPICalled")
	t.Cleanup(func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		for _, e := range g.errs {
			t.Errorf("page error: %s", e)
		}
	})
	return r, g
}

func (g *gui) collect(p *cdp.Page, method string) {
	events, stop := p.Subscribe(method, 64)
	defer stop()
	for {
		select {
		case <-g.ctx.Done():
			return
		case e := <-events:
			var ev struct {
				Type             string                `json:"type"`
				Args             []struct{ Value any } `json:"args"`
				ExceptionDetails struct {
					Text      string `json:"text"`
					Exception struct {
						Description string `json:"description"`
					} `json:"exception"`
				} `json:"exceptionDetails"`
			}
			_ = json.Unmarshal(e.Params, &ev)
			msg := ""
			switch {
			case method == "Runtime.exceptionThrown":
				msg = ev.ExceptionDetails.Text + " " + ev.ExceptionDetails.Exception.Description
			case ev.Type == "error":
				msg = fmt.Sprint(ev.Args)
			default:
				continue
			}
			g.mu.Lock()
			g.errs = append(g.errs, msg)
			g.mu.Unlock()
		}
	}
}

func (g *gui) eval(expr string, out any) {
	g.t.Helper()
	if err := g.p.Eval(g.ctx, expr, out); err != nil {
		g.t.Fatalf("%s: %v", expr, err)
	}
}

func (g *gui) waitFor(expr string) {
	g.t.Helper()
	ctx, cancel := context.WithTimeout(g.ctx, 15*time.Second)
	defer cancel()
	if err := g.p.WaitFor(ctx, expr); err != nil {
		var body string
		_ = g.p.Eval(g.ctx, `(async () => location.href + " (" + document.readyState + ", sw " + Boolean(navigator.serviceWorker.controller) + ")\n" + document.body.innerText.slice(0, 600) + "\nresources: " + JSON.stringify(performance.getEntriesByType("resource").map((e) => [e.name, e.responseStatus, e.transferSize])) + "\nmain.js: " + await fetch("/main.js").then((r) => r.status + " " + r.headers.get("content-type"), (e) => String(e)))()`, &body)
		g.t.Fatalf("%v\npage: %s", err, body)
	}
}

func (g *gui) navigate(url string) {
	g.t.Helper()
	if err := g.p.Navigate(g.ctx, url); err != nil {
		g.t.Fatal(err)
	}
}

func (g *gui) key(name string, modifiers int) {
	g.t.Helper()
	if err := g.p.Key(g.ctx, name, modifiers); err != nil {
		g.t.Fatal(err)
	}
}

// focusType focuses a field, empties it and types text into it.
func (g *gui) focusType(selector, text string) {
	g.t.Helper()
	g.eval(fmt.Sprintf(`(() => { const el = document.querySelector(%q); el.focus(); el.select && el.select(); })()`, selector), nil)
	if err := g.p.Type(g.ctx, text); err != nil {
		g.t.Fatal(err)
	}
}

// choose selects an option of a select, as a person does.
func (g *gui) choose(selector, value string) {
	g.t.Helper()
	g.eval(fmt.Sprintf(`(() => { const el = document.querySelector(%q); el.value = %q; el.dispatchEvent(new Event("change", { bubbles: true })); })()`, selector, value), nil)
}

// show opens a screen and waits until it has loaded.
func (g *gui) show(id string) {
	g.t.Helper()
	g.eval(fmt.Sprintf(`location.hash = "#/%s"`, id), nil)
	g.waitFor(`document.querySelector("main h1") && !document.querySelector("main p.muted[role=status]")`)
}

func (g *gui) signInWithCode(role string) {
	g.t.Helper()
	resp, body := g.sock.do("POST", api.PathSessionCodes, api.SessionCodeRequest{Role: role})
	var code api.SessionCode
	if resp.code != 200 || json.Unmarshal(body, &code) != nil {
		g.t.Fatalf("code: %d %s", resp.code, body)
	}
	g.navigate(code.URL)
	g.waitFor(`document.querySelector("nav.sidebar")`)
}

type finding struct {
	Kind  string  `json:"kind"`
	Text  string  `json:"text"`
	Ratio float64 `json:"ratio"`
	Need  float64 `json:"need"`
	FG    string  `json:"fg"`
	BG    string  `json:"bg"`
}

// problems checks the page as it is now: structure, contrast and the
// names of every control in the accessibility tree.
func (g *gui) problems(lang string) []string {
	g.t.Helper()
	var found []finding
	g.eval(fmt.Sprintf(`[...window.__glAudit.structure(%q), ...window.__glAudit.contrast()]`, lang), &found)
	var out []string
	for _, f := range found {
		if f.Kind == "contrast" {
			out = append(out, fmt.Sprintf("contrast %q ratio %.2f < %.1f (fg %s, bg %s)", f.Text, f.Ratio, f.Need, f.FG, f.BG))
			continue
		}
		out = append(out, fmt.Sprintf("%s %q", f.Kind, f.Text))
	}
	nodes, err := g.p.AXTree(g.ctx)
	if err != nil {
		g.t.Fatal(err)
	}
	for _, n := range nodes {
		role, name := n.RoleName()
		if !n.Ignored && slices.Contains(namedRoles, strings.ToLower(role)) && strings.TrimSpace(name) == "" {
			out = append(out, fmt.Sprintf("%s without an accessible name (DOM node %d)", role, n.BackendDOMNodeID))
		}
	}
	return out
}

func (g *gui) audit(where, lang string) {
	g.t.Helper()
	for _, p := range g.problems(lang) {
		g.t.Errorf("%s: %s", where, p)
	}
}

type focused struct {
	Tag       string  `json:"tag"`
	ID        string  `json:"id"`
	Href      string  `json:"href"`
	Text      string  `json:"text"`
	Visible   bool    `json:"visible"`
	Outline   bool    `json:"outline"`
	Indicator float64 `json:"indicator"`
}

func (g *gui) focus() focused {
	g.t.Helper()
	var f focused
	g.eval("window.__glAudit.focus()", &f)
	return f
}

// walkFocus moves the focus with Tab from the skip button to the end of the
// page, checks that every stop shows where the focus is, and returns the
// routes of the links it passed.
func (g *gui) walkFocus(where string) []string {
	g.t.Helper()
	g.eval(`document.querySelector(".skip").focus()`, nil)
	var routes []string
	for range 400 {
		g.key("Tab", 0)
		f := g.focus()
		if f.Tag == "BODY" {
			return routes
		}
		if !f.Visible || !f.Outline || f.Indicator < 3 {
			g.t.Errorf("%s: focus indicator of %s %q: visible %v, outline %v, contrast %.2f", where, f.Tag, f.Text, f.Visible, f.Outline, f.Indicator)
		}
		if strings.HasPrefix(f.Href, "#/") {
			routes = append(routes, f.Href)
		}
	}
	g.t.Errorf("%s: the focus does not leave the page after 400 stops", where)
	return routes
}

func TestInterfaceEndToEnd(t *testing.T) {
	r, g := startGUI(t, 50*time.Millisecond)

	t.Run("sign in with a token", func(t *testing.T) {
		g.t = t
		resp, body := g.sock.do("POST", api.PathTokens, api.CreateToken{Name: "e2e", Role: "admin"})
		var tok api.CreatedToken
		if resp.code != 200 && resp.code != 201 || json.Unmarshal(body, &tok) != nil {
			t.Fatalf("token: %d %s", resp.code, body)
		}
		g.navigate(g.base + "/")
		g.waitFor(`document.getElementById("token")`)
		g.audit("login", "en")
		g.focusType("#token", "greenlens_000000000000_"+strings.Repeat("a", 43))
		g.key("Enter", 0)
		g.waitFor(`document.getElementById("login-error")`)
		g.focusType("#token", tok.Secret)
		g.key("Enter", 0)
		g.waitFor(`document.querySelector("nav.sidebar")`)
		var who string
		g.eval(`document.querySelector(".sidebar-foot").innerText`, &who)
		if !strings.Contains(who, "admin") {
			t.Fatalf("session: %q", who)
		}
	})

	t.Run("sign out and in with a one-time link", func(t *testing.T) {
		g.t = t
		g.eval(`[...document.querySelectorAll(".sidebar-foot button")].find((b) => b.textContent.includes("Sign out")).click()`, nil)
		g.waitFor(`document.getElementById("token")`)
		g.signInWithCode("viewer")
		var hasConfig bool
		g.eval(`Boolean(document.querySelector('nav a[href="#/config"]'))`, &hasConfig)
		if hasConfig {
			t.Error("a viewer sees the configuration")
		}
		// A link works once.
		resp, body := g.sock.do("POST", api.PathSessionCodes, api.SessionCodeRequest{Role: "admin"})
		var code api.SessionCode
		if resp.code != 200 || json.Unmarshal(body, &code) != nil {
			t.Fatalf("code: %d %s", resp.code, body)
		}
		g.navigate(code.URL)
		g.waitFor(`document.querySelector("nav.sidebar")`)
		g.navigate(code.URL)
		g.waitFor(`location.hash.includes("expired=1") || document.querySelector("nav.sidebar")`)
	})

	t.Run("the checks find problems", func(t *testing.T) {
		g.t = t
		g.signInWithCode("admin")
		g.show("about")
		g.eval(`(() => {
			const m = document.querySelector("main");
			m.insertAdjacentHTML("beforeend", '<div id="planted"><p>faint</p><button type="button"></button><h4>deep</h4></div>');
			// The policy of the page refuses style attributes in markup.
			document.querySelector("#planted p").style.color = "var(--bg-deep)";
		})()`, nil)
		got := strings.Join(g.problems("en"), "\n")
		for _, want := range []string{`contrast "faint"`, "button without an accessible name", `heading-skip "H4 deep"`} {
			if !strings.Contains(got, want) {
				t.Errorf("planted problem %q not found in:\n%s", want, got)
			}
		}
		g.eval(`document.getElementById("planted").remove()`, nil)
		if p := g.problems("en"); len(p) > 0 {
			t.Errorf("problems left after removing the planted ones: %v", p)
		}
	})

	t.Run("every screen in both themes", func(t *testing.T) {
		g.t = t
		for _, theme := range []string{"dark", "light"} {
			g.choose("#theme", theme)
			g.waitFor(fmt.Sprintf(`document.documentElement.dataset.theme === %q`, theme))
			for _, id := range screenIDs {
				g.show(id)
				var title string
				var errors int
				g.eval(`document.title`, &title)
				g.eval(`document.querySelectorAll("main .notice-error").length`, &errors)
				if !strings.HasSuffix(title, " - GreenLens") || errors > 0 {
					t.Errorf("%s: title %q, %d errors", id, title, errors)
				}
				g.audit(id+" "+theme, "en")
				g.walkFocus(id + " " + theme)
			}
			g.show("help/guides/gui")
			g.audit("help page "+theme, "en")
		}
		g.choose("#theme", "system")
		for _, scheme := range []string{"light", "dark"} {
			if err := g.p.Call(g.ctx, "Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{{"name": "prefers-color-scheme", "value": scheme}}}, nil); err != nil {
				t.Fatal(err)
			}
			g.waitFor(fmt.Sprintf(`document.documentElement.dataset.theme === %q`, scheme))
		}
	})

	t.Run("keyboard", func(t *testing.T) {
		g.t = t
		g.show("models")
		g.eval(`document.activeElement && document.activeElement.blur(); window.scrollTo(0, 0)`, nil)
		g.key("Tab", 0)
		var skip bool
		g.eval(`document.activeElement.classList.contains("skip")`, &skip)
		if f := g.focus(); !skip || !f.Visible || !f.Outline {
			t.Fatalf("first stop is not the visible skip button: %+v", f)
		}
		g.key("Enter", 0)
		var hash, active string
		g.eval(`location.hash`, &hash)
		g.eval(`document.activeElement.id`, &active)
		if hash != "#/models" || active != "main" {
			t.Fatalf("skip button: hash %q, focus %q", hash, active)
		}
		g.show("summary")
		nav := g.walkFocus("summary")
		for _, id := range screenIDs {
			if id != "welcome" && !slices.Contains(nav, "#/"+id) {
				t.Errorf("the navigation link of %s is not reached with Tab: %v", id, nav)
			}
		}
		// Enter on a link of the navigation opens its screen.
		g.eval(`document.querySelector('nav a[href="#/sources"]').focus()`, nil)
		g.key("Enter", 0)
		g.waitFor(`location.hash === "#/sources"`)
		g.key("Tab", cdp.Shift)
		if f := g.focus(); f.Tag == "BODY" {
			t.Error("Shift+Tab lost the focus")
		}
	})

	t.Run("narrow screens", func(t *testing.T) {
		g.t = t
		if err := g.p.Call(g.ctx, "Emulation.setDeviceMetricsOverride", map[string]any{"width": 375, "height": 812, "deviceScaleFactor": 2, "mobile": true}, nil); err != nil {
			t.Fatal(err)
		}
		for _, id := range screenIDs {
			g.show(id)
			// Charts take their width from a resize observer, which runs
			// with the next frames.
			g.eval(`new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done)))`, nil)
			var wide []string
			g.eval(`[...document.querySelectorAll("main *")].filter((el) => el.getBoundingClientRect().right > innerWidth + 1 && !el.closest(".table-wrap, pre")).slice(0, 3).map((el) => el.tagName + "." + el.className)`, &wide)
			var scroll int
			g.eval(`document.documentElement.scrollWidth - innerWidth`, &scroll)
			if scroll > 0 || len(wide) > 0 {
				var sizes string
				g.eval(`JSON.stringify([".instrument-chart", ".instrument-chart > div", ".uplot", "main"].map((s) => { const e = document.querySelector(s); return e ? [s, e.getBoundingClientRect().left, e.getBoundingClientRect().width, e.clientWidth] : [s]; }))`, &sizes)
				t.Errorf("%s at 375 px: %d px of horizontal scroll, wide elements %v %s", id, scroll, wide, sizes)
			}
		}
		if err := g.p.Call(g.ctx, "Emulation.setDeviceMetricsOverride", map[string]any{"width": 1280, "height": 900, "deviceScaleFactor": 1, "mobile": false}, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("charts come with tables", func(t *testing.T) {
		g.t = t
		g.show("summary")
		g.waitFor(`[...document.querySelectorAll("main button")].some((b) => b.textContent.includes("Show as a table"))`)
		g.eval(`[...document.querySelectorAll("main button")].find((b) => b.textContent.includes("Show as a table")).focus()`, nil)
		g.key("Enter", 0)
		g.waitFor(`document.querySelector("main table caption") && document.querySelectorAll("main table tbody tr").length > 0`)
		g.audit("summary table", "en")
	})

	t.Run("benchmark", func(t *testing.T) {
		g.t = t
		g.show("benchmarks")
		g.waitFor(`document.getElementById("bench-server")`)
		g.focusType("#bench-model", "qwen2.5:0.5b")
		g.focusType("#bench-runs", "2")
		g.focusType("#bench-tokens", "8")
		g.focusType("#bench-warmup", "0")
		g.focusType("#bench-minrun", "0")
		g.eval(`document.querySelector('main form button[type="submit"]').focus()`, nil)
		g.key("Enter", 0)
		g.waitFor(`[...document.querySelectorAll("main [role=status]")].some((s) => s.textContent.includes("Benchmark completed"))`)
		resp, body := g.sock.do("GET", api.PathBenchmarks, nil)
		var list api.Benchmarks
		if resp.code != 200 || json.Unmarshal(body, &list) != nil || len(list.Benchmarks) != 1 || list.Benchmarks[0].Target.Model != "qwen2.5:0.5b" {
			t.Fatalf("benchmarks: %d %s", resp.code, body)
		}
		g.audit("benchmarks after a run", "en")
	})

	t.Run("efficient mode", func(t *testing.T) {
		g.t = t
		g.show("summary")
		g.choose("#efficient", "on")
		g.waitFor(`document.documentElement.classList.contains("efficient")`)
		var filter string
		g.eval(`getComputedStyle(document.querySelector(".glass")).backdropFilter`, &filter)
		if filter != "none" {
			t.Errorf("glass keeps its blur in efficient mode: %q", filter)
		}
		g.audit("efficient", "en")
		g.choose("#efficient", "off")
		g.waitFor(`!document.documentElement.classList.contains("efficient")`)
		g.eval(`getComputedStyle(document.querySelector(".glass")).backdropFilter`, &filter)
		if filter == "none" {
			t.Error("glass without blur outside efficient mode")
		}
	})

	t.Run("language", func(t *testing.T) {
		g.t = t
		g.choose("#lang", "es")
		g.waitFor(`document.documentElement.lang === "es" && document.querySelector('nav a[href="#/summary"]').textContent.includes("Resumen")`)
		g.show("summary")
		g.audit("summary es", "es")
		g.choose("#lang", "en")
		g.waitFor(`document.documentElement.lang === "en"`)
	})

	t.Run("budgets", func(t *testing.T) {
		g.t = t
		for _, c := range []struct {
			method string
			params map[string]any
		}{
			{"Network.setCacheDisabled", map[string]any{"cacheDisabled": true}},
			{"Network.setBypassServiceWorker", map[string]any{"bypass": true}},
		} {
			if err := g.p.Call(g.ctx, c.method, c.params, nil); err != nil {
				t.Fatal(err)
			}
		}
		finished, stop := g.p.Subscribe("Network.loadingFinished", 256)
		defer stop()
		g.eval(`localStorage.setItem("greenlens.welcome", "done")`, nil)
		g.navigate("about:blank")
		g.navigate(g.base + "/#/summary")
		g.waitFor(`document.querySelector("main .metric-value") && /\d/.test(document.querySelector("main .metric-value").textContent)`)
		var rendered float64
		g.eval(`performance.now()`, &rendered)
		total := 0.0
		for len(finished) > 0 {
			var e struct {
				EncodedDataLength float64 `json:"encodedDataLength"`
			}
			_ = json.Unmarshal((<-finished).Params, &e)
			total += e.EncodedDataLength
		}
		t.Logf("first visit: %.1f KB until the first value, %.0f ms", total/1024, rendered)
		if total > loadBudget {
			t.Errorf("first visit downloads %.1f KB, over %d KB", total/1024, loadBudget>>10)
		}
		if time.Duration(rendered*float64(time.Millisecond)) > renderBudget {
			t.Errorf("first value after %.0f ms, over %s", rendered, renderBudget)
		}
		for _, c := range []struct {
			method string
			params map[string]any
		}{
			{"Network.setCacheDisabled", map[string]any{"cacheDisabled": false}},
			{"Network.setBypassServiceWorker", map[string]any{"bypass": false}},
		} {
			if err := g.p.Call(g.ctx, c.method, c.params, nil); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("installed window without the agent", func(t *testing.T) {
		g.t = t
		g.eval(`navigator.serviceWorker.ready.then(() => true)`, nil)
		if err := g.p.Reload(g.ctx); err != nil {
			t.Fatal(err)
		}
		g.waitFor(`navigator.serviceWorker.controller && document.querySelector("nav.sidebar")`)
		g.choose("#lang", "es")
		g.waitFor(`document.documentElement.lang === "es"`)
		r.stop(t)
		if err := g.p.Reload(g.ctx); err != nil {
			t.Fatal(err)
		}
		g.waitFor(`document.querySelector("main .notice-error") && document.querySelector("main button")`)
		var text string
		g.eval(`document.querySelector("main").innerText`, &text)
		if !strings.Contains(text, "el agente no responde") || !strings.Contains(text, "Reintentar") {
			t.Fatalf("without the agent: %q", text)
		}
		// The messages of the page while the agent is gone are expected.
		g.mu.Lock()
		g.errs = nil
		g.mu.Unlock()
	})
}

// frameBudget is one frame at 60 Hz: an update of the charts that takes
// longer than that drops frames.
const frameBudget = time.Second / 60

// TestChartsWithFifteenMinutesOfData fills the live charts with as many
// samples as 15 minutes at 1 s give, arriving much faster than that, and
// checks that each update of the page fits in a frame.
func TestChartsWithFifteenMinutesOfData(t *testing.T) {
	r, g := startGUI(t, 20*time.Millisecond)
	g.signInWithCode("admin")
	g.eval(`localStorage.setItem("greenlens.welcome", "done")`, nil)
	g.choose("#efficient", "off")
	g.show("summary")
	before := waitSamples(t, r, 1).Samples
	deadline := time.Now().Add(60 * time.Second)
	for {
		if s := waitSamples(t, r, 1).Samples; s-before >= 15*60+60 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the agent took over a minute for 960 samples")
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err := g.p.Call(g.ctx, "Performance.enable", nil, nil); err != nil {
		t.Fatal(err)
	}
	busy := func() float64 {
		var m struct {
			Metrics []struct {
				Name  string  `json:"name"`
				Value float64 `json:"value"`
			} `json:"metrics"`
		}
		if err := g.p.Call(g.ctx, "Performance.getMetrics", nil, &m); err != nil {
			t.Fatal(err)
		}
		for _, x := range m.Metrics {
			if x.Name == "TaskDuration" {
				return x.Value
			}
		}
		t.Fatal("no TaskDuration metric")
		return 0
	}
	g.eval(`window.__long = 0; new PerformanceObserver((l) => { window.__long += l.getEntries().length; }).observe({ type: "longtask" })`, nil)
	s0, b0 := waitSamples(t, r, 1).Samples, busy()
	time.Sleep(5 * time.Second)
	s1, b1 := waitSamples(t, r, 1).Samples, busy()
	var long int
	g.eval(`window.__long`, &long)
	per := time.Duration((b1 - b0) / float64(s1-s0) * float64(time.Second))
	t.Logf("%d updates in 5 s with more than 900 points: %s of main thread each, %d long tasks", s1-s0, per.Round(10*time.Microsecond), long)
	if per > frameBudget || long > 0 {
		t.Errorf("updates take %s each, over %s, with %d long tasks", per, frameBudget, long)
	}
}
