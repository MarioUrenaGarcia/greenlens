// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package tui

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MarioUrenaGarcia/greenlens/internal/alerts"
	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/i18n"
	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

var update = flag.Bool("update", false, "rewrite the golden files")

var t0 = time.Date(2026, 10, 20, 18, 30, 0, 0, time.UTC)

func status() api.Status {
	return api.Status{
		SchemaVersion: 1, Version: "v0.3.0", StartedAt: t0.Add(-time.Hour), Samples: 3600,
		Domains: []api.Domain{
			{Domain: source.DomainPackage, Level: "component", PowerW: 42.5, EnergyJ: 152000, Quality: source.QualityMeasured},
			{Domain: source.DomainCore, Level: "contained", PowerW: 30, EnergyJ: 100000, Quality: source.QualityMeasured},
			{Domain: source.DomainGPU, Level: "component", PowerW: 78.25, EnergyJ: 260000, Quality: source.QualityMeasured},
		},
		Channels: []api.Channel{
			{ID: "rapl:package-0", Source: "rapl", Domain: source.DomainPackage, Quality: source.QualityMeasured, Selected: true, Reason: "selector.reason.selected"},
			{ID: "nvml:0000:01:00.0", Source: "nvml", Domain: source.DomainGPU, Quality: source.QualityMeasured, Selected: true, Reason: "selector.reason.selected"},
			{ID: "nvidia_smi:0000:01:00.0", Source: "nvidia_smi", Domain: source.DomainGPU, Quality: source.QualityMeasured, Reason: "selector.reason.duplicate", ReasonArgs: map[string]string{"source": "nvml"}},
		},
		Problems: []api.Problem{{Code: "GL1003", Args: map[string]string{"paranoid": "4"}, Channel: "perf_event:package-0"}},
		Backends: []api.Backend{
			{Name: "ollama-11434", Kind: "ollama", PowerW: 61.2, EnergyJ: 38500, TokenCounters: true, Models: []api.Model{
				{Name: "qwen2.5:0.5b", EnergyJ: 36000, PromptTokens: 52000, GeneratedTokens: 152000, JoulesPerToken: 0.237},
				{Name: "llama3.2:3b", EnergyJ: 2500, PromptTokens: 900, GeneratedTokens: 2000, JoulesPerToken: 1.25},
			}},
			{Name: "llamacpp-18080", Kind: "llamacpp", PowerW: 9.4, EnergyJ: 3352, TokenCounters: true, Models: []api.Model{
				{Name: "qwen2.5-0.5b-instruct-q4_k_m.gguf", EnergyJ: 3352, PromptTokens: 306, GeneratedTokens: 36956, JoulesPerToken: 0.0907},
			}},
		},
	}
}

func live() api.Live {
	var l api.Live
	for i := range 120 {
		at := t0.Add(time.Duration(i-120) * time.Second)
		gpu := 24.0
		if i > 40 {
			gpu = 80 + float64(i%7)
		}
		l.Points = append(l.Points, api.LivePoint{Time: at, IntervalMS: 1000, PowerW: map[source.Domain]float64{
			source.DomainPackage: 30 + float64(i%11), source.DomainGPU: gpu, source.DomainCore: 20,
		}})
	}
	return l
}

func consumers() api.BackendUsage {
	return api.BackendUsage{SchemaVersion: 1, GroupBy: store.ByConsumer, Rows: []store.UsageEnergy{
		{Backend: "ollama-11434", Kind: "ollama", Consumer: "team-a", EnergyJ: 20000, PromptTokens: 30000, GeneratedTokens: 90000, Counted: true},
		{Backend: "ollama-11434", Kind: "ollama", Consumer: "consumer-3bd8cd7106ed", EnergyJ: 16000, PromptTokens: 22000, GeneratedTokens: 62000, Counted: true},
		{Backend: "llamacpp-18080", Kind: "llamacpp", EnergyJ: 3352},
	}}
}

type fake struct {
	err   error
	price *float64
}

func (f fake) Status(context.Context) (api.Status, error) {
	st := status()
	// The agent adds up the indicators of every interval; with constant
	// factors they are those of the whole energy.
	fs := indicators.Default()
	fs.TariffPerKWh = f.price
	st.Factors = api.Factors{EmissionGPerKWh: fs.EmissionGPerKWh, PUE: fs.PUE, Currency: fs.Currency, TariffPerKWh: f.price, TimeZone: "America/Mexico_City"}
	for i := range st.Backends {
		b := &st.Backends[i]
		b.Indicators = fs.Compute(b.EnergyJ)
		for j := range b.Models {
			b.Models[j].Indicators = fs.Compute(b.Models[j].EnergyJ)
		}
	}
	return st, f.err
}
func (f fake) Live(context.Context) (api.Live, error) { return live(), f.err }
func (f fake) BackendUsage(context.Context, time.Time, time.Time, string) (api.BackendUsage, error) {
	return consumers(), f.err
}

func (f fake) Alerts(context.Context, time.Time, time.Time) (api.Alerts, error) {
	fired, started, resolved := t0.Add(-12*time.Minute), t0.Add(-3*time.Hour), t0.Add(-2*time.Hour)
	firedOld := started.Add(5 * time.Minute)
	return api.Alerts{SchemaVersion: 1, EveryS: 15,
		Active: []alerts.Alert{
			{ID: 3, Rule: "high_jpt", Expr: alerts.ModelJPerToken, Op: ">", Threshold: 5, Labels: map[string]string{"backend": "ollama-11434", "model": "qwen2.5:7b"},
				State: alerts.Firing, Value: 6.214, StartedAt: fired.Add(-10 * time.Minute), FiredAt: &fired},
			{ID: 4, Rule: "node_busy", Expr: alerts.NodePowerW, Op: ">", Threshold: 300, Labels: map[string]string{}, State: alerts.Pending, Value: 312.5, StartedAt: t0.Add(-time.Minute)},
		},
		History: []alerts.Alert{
			{ID: 2, Rule: "vllm_busy", Expr: alerts.BackendPowerW, Op: ">", Threshold: 40, Labels: map[string]string{"backend": "vllm-8000"},
				State: alerts.Resolved, Value: 38.1, StartedAt: started, FiredAt: &firedOld, ResolvedAt: &resolved},
		}}, f.err
}

func localizer(t *testing.T, lang string) *i18n.Localizer {
	t.Helper()
	l, err := i18n.New(lang)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func newModel(t *testing.T, lang string, color, unicode bool) *model {
	t.Helper()
	m := New(Options{Source: fake{}, L: localizer(t, lang), Color: color, Unicode: unicode,
		Now: func() time.Time { return t0 }}).(*model)
	m.Update(tea.WindowSizeMsg{Width: MinWidth, Height: MinHeight})
	return m
}

// load runs the fetch of the model and hands it the result.
func load(m *model) {
	m.Update(m.fetch()())
}

func press(m *model, keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "tab":
			msg = tea.KeyPressMsg{Code: tea.KeyTab}
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "backspace":
			msg = tea.KeyPressMsg{Code: tea.KeyBackspace}
		default:
			r := []rune(k)[0]
			msg = tea.KeyPressMsg{Code: r, Text: k}
		}
		_, cmd = m.Update(msg)
		if cmd != nil {
			if d, ok := cmd().(dataMsg); ok {
				m.Update(d)
			}
		}
	}
	return cmd
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v; run go test ./internal/tui -update", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from %s:\n%s", name, p, got)
	}
}

// plain removes escape sequences, to measure what is on screen.
func plain(s string) string {
	var b strings.Builder
	esc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			esc = true
		case esc && (r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'):
			esc = false
		case !esc:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestSnapshots(t *testing.T) {
	modes := []struct {
		name           string
		color, unicode bool
	}{{"color", true, true}, {"mono", false, true}, {"ascii", false, false}}
	for _, mode := range modes {
		for _, lang := range []string{"en", "es"} {
			m := newModel(t, lang, mode.color, mode.unicode)
			load(m)
			for v := range viewCount {
				m.setView(v)
				load(m)
				out := m.View().Content
				name := strings.Join([]string{viewKeys[v], mode.name, lang}, "-")
				golden(t, name, out)
				lines := strings.Split(plain(out), "\n")
				if len(lines) != MinHeight {
					t.Errorf("%s: %d lines", name, len(lines))
				}
				for i, l := range lines {
					if w := len([]rune(l)); w > MinWidth {
						t.Errorf("%s: line %d is %d wide: %q", name, i, w, l)
					}
					if !mode.unicode {
						for _, r := range l {
							if r > 0x7e {
								t.Errorf("%s: line %d has %q in ASCII mode", name, i, r)
								break
							}
						}
					}
				}
				if !mode.color && strings.Contains(out, "\x1b[38;") {
					t.Errorf("%s: colors in monochrome", name)
				}
			}
			press(m, "?")
			golden(t, "help-"+mode.name+"-"+lang, m.View().Content)
		}
	}
}

func TestKeys(t *testing.T) {
	m := newModel(t, "en", false, false)
	load(m)
	press(m, "2")
	if m.view != viewBackends || len(m.tbl.Rows()) != 5 {
		t.Fatalf("view %d with %d rows", m.view, len(m.tbl.Rows()))
	}
	// Sorting by power keeps the models under their backend.
	press(m, "s")
	rows := m.tbl.Rows()
	if !strings.HasPrefix(rows[0][0], "ollama") || !strings.HasPrefix(rows[1][0], "  ") || !strings.HasPrefix(rows[3][0], "llamacpp") {
		t.Errorf("sorted by power: %v", rows)
	}
	// The filter keeps a model and its backend.
	press(m, "/", "l", "l", "a", "m", "a", "3", "enter")
	rows = m.tbl.Rows()
	if len(rows) != 2 || !strings.Contains(rows[1][0], "llama3.2") {
		t.Errorf("filtered: %v", rows)
	}
	press(m, "/", "esc")
	if len(m.tbl.Rows()) != 5 {
		t.Errorf("filter not cleared: %d rows", len(m.tbl.Rows()))
	}
	press(m, "tab")
	if m.view != viewConsumers || len(m.tbl.Rows()) != 2 {
		t.Errorf("consumers: view %d, %d rows", m.view, len(m.tbl.Rows()))
	}
	press(m, "p")
	if !m.paused || !strings.Contains(m.View().Content, "paused") {
		t.Error("pause")
	}
	if _, cmd := m.Update(tickMsg(t0)); cmd == nil {
		t.Error("the tick stops while paused")
	}
	if cmd := press(m, "q"); cmd == nil || cmd() != tea.Quit() {
		t.Error("q does not quit")
	}
}

func TestExport(t *testing.T) {
	var name string
	var got [][]string
	m := newModel(t, "en", false, false)
	m.o.Export = func(n string, rows [][]string) (string, error) {
		name, got = n, rows
		return "greenlens-x.csv", nil
	}
	load(m)
	press(m, "e")
	if name != "node" || len(got) != 121 || got[0][0] != "time" || got[0][1] != "package_w" {
		t.Fatalf("node export %s %v", name, got[:1])
	}
	press(m, "2", "e")
	// Rows are sorted by name: the llama.cpp backend first.
	if name != "backends" || len(got) != 6 || got[0][0] != "backend_model" || got[5][0] != "ollama-11434/qwen2.5:0.5b" || got[5][3] != "0.237" {
		t.Fatalf("backends export %v", got)
	}
	if !strings.Contains(m.View().Content, "greenlens-x.csv") {
		t.Error("the export is not reported")
	}
	m.o.Export = func(string, [][]string) (string, error) { return "", errors.New("disk full") }
	press(m, "e")
	if !strings.Contains(m.View().Content, "disk full") {
		t.Error("the failure is not reported")
	}
}

func TestStates(t *testing.T) {
	m := newModel(t, "es", false, false)
	if !strings.Contains(m.View().Content, "Esperando") {
		t.Errorf("before the first reading: %s", m.View().Content)
	}
	m.Update(dataMsg{err: errors.New("dial unix /run/greenlens/greenlens.sock: connect: no such file")})
	if !strings.Contains(m.View().Content, "No se puede contactar al agente") {
		t.Errorf("unreachable: %s", m.View().Content)
	}
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	if !strings.Contains(m.View().Content, "80 por 24") {
		t.Errorf("too small: %s", m.View().Content)
	}
	// Tokens per second come from two readings.
	m = newModel(t, "en", false, false)
	load(m)
	st := status()
	st.Backends[0].Models[0].GeneratedTokens += 300
	m.Update(dataMsg{at: t0.Add(2 * time.Second), status: st, live: live()})
	if r := m.rates["ollama-11434/qwen2.5:0.5b"]; r != 150 {
		t.Errorf("rate %v", r)
	}
}

func TestChart(t *testing.T) {
	th := newTheme(false, true)
	rows := chart(th, []series{{name: "gpu", values: []float64{0, 50, 100}}}, 4, 2, 100)
	if len(rows) != 2 || len([]rune(rows[0])) != 4 {
		t.Fatalf("%q", rows)
	}
	// Newest values at the right edge, the highest at the top.
	if []rune(rows[0])[3] == ' ' || []rune(rows[1])[0] != ' ' {
		t.Errorf("%q", rows)
	}
	if got := resample([]float64{1, 2, 3, 4}, 2); got[0] != 1.5 || got[1] != 3.5 {
		t.Errorf("resample %v", got)
	}
	for in, want := range map[float64]float64{0: 1, 3: 5, 42.5: 50, 130: 200, 0.07: 0.1} {
		if got := niceTop(in); got != want {
			t.Errorf("niceTop(%v) = %v", in, got)
		}
	}
	if truncate("qwen2.5-0.5b-instruct-q4_k_m.gguf", 10) != "qwen2.5..." || truncate("abc", 10) != "abc" {
		t.Error("truncate")
	}
}

func TestASCIIFold(t *testing.T) {
	if got := asciiFold("¿Energía desde el inicio? Año ÚLTIMO"); got != "Energia desde el inicio? Ano ULTIMO" {
		t.Errorf("%q", got)
	}
}

func TestCostColumnFits(t *testing.T) {
	price := 2.5
	m := newModel(t, "es", false, false)
	m.o.Source = fake{price: &price}
	load(m)
	press(m, "2")
	out := plain(m.View().Content)
	for i, l := range strings.Split(out, "\n") {
		if len([]rune(l)) > MinWidth {
			t.Errorf("line %d is %d wide: %q", i, len([]rune(l)), l)
		}
	}
	if !strings.Contains(out, "Costo") || !strings.Contains(out, "MXN") {
		t.Errorf("no cost column:\n%s", out)
	}
}
