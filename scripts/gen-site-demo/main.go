// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Command gen-site-demo writes the demo of the graphical interface for the
// documentation site: the interface itself, a script that answers its API
// in the browser, and a simulated workspace for it to show. Nothing comes
// from a real machine: the backends, models, consumers, hardware and every
// value are generated with a fixed seed, over the 35 days before the hour
// the demo is built and the week after it, so the page can move the data
// by whole weeks to the day it is opened. The texts, the help and the
// printable reports come from the same code as those of the agent. Run it
// with "make site".
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"math"
	"math/rand/v2"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/evanw/esbuild/pkg/api"

	"github.com/MarioUrenaGarcia/greenlens/internal/about"
	"github.com/MarioUrenaGarcia/greenlens/internal/agent"
	gapi "github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/bench"
	"github.com/MarioUrenaGarcia/greenlens/internal/discovery"
	"github.com/MarioUrenaGarcia/greenlens/internal/i18n"
	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/report"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/stats"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
	"github.com/MarioUrenaGarcia/greenlens/web"
)

const (
	days     = 35
	ahead    = 7 * 24
	timeZone = "America/Mexico_City"
	jsBanner = `/* SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (C) 2026 Mario Ureña García */`
)

// model is a simulated model: its tokens per hour at full activity, its
// energy per generated token and the part of its energy drawn by the GPU.
type model struct {
	backend, kind, endpoint, name string
	rate, jpt, gpu                float64
	// worseFrom, when set, is the day from which its energy per token
	// grows by worse, after its file changed.
	worseFrom int
	worse     float64
}

var models = []model{
	{backend: "ollama-11434", kind: "ollama", endpoint: "http://127.0.0.1:11434", name: "qwen2.5:7b", rate: 180000, jpt: 0.32, gpu: 0.88},
	{backend: "ollama-11434", kind: "ollama", endpoint: "http://127.0.0.1:11434", name: "llama3.2:3b", rate: 280000, jpt: 0.16, gpu: 0.85, worseFrom: days - 4, worse: 1.28},
	{backend: "vllm-8000", kind: "vllm", endpoint: "http://127.0.0.1:8000", name: "Qwen/Qwen2.5-14B-Instruct", rate: 440000, jpt: 0.45, gpu: 0.9},
	{backend: "vllm-8000", kind: "vllm", endpoint: "http://127.0.0.1:8000", name: "Qwen/Qwen2.5-Coder-7B-Instruct", rate: 120000, jpt: 0.27, gpu: 0.9},
	{backend: "llamacpp-8080", kind: "llamacpp", endpoint: "http://127.0.0.1:8080", name: "gemma-3-4b-it-Q4_K_M.gguf", rate: 50000, jpt: 0.85, gpu: 0.05},
}

// ModelData is the use of one model, hour by hour.
type ModelData struct {
	Backend   string    `json:"backend"`
	Kind      string    `json:"kind"`
	Endpoint  string    `json:"endpoint"`
	Model     string    `json:"model"`
	EnergyJ   []float64 `json:"energy_j"`
	GPUJ      []float64 `json:"gpu_j"`
	Generated []float64 `json:"generated"`
	Prompt    []float64 `json:"prompt"`
	Requests  []float64 `json:"requests"`
}

// Consumer is the share of every model that one consumer uses.
type Consumer struct {
	Name  string  `json:"name"`
	Share float64 `json:"share"`
}

// Dataset is the simulated workspace. Hour i covers the hour that starts
// Hours - i hours before End; the series hold Ahead more hours after End.
type Dataset struct {
	SchemaVersion int                   `json:"schema_version"`
	End           time.Time             `json:"end"`
	Hours         int                   `json:"hours"`
	Ahead         int                   `json:"ahead_hours"`
	Domains       map[string][]float64  `json:"domains_w"`
	Models        []ModelData           `json:"models"`
	Consumers     []Consumer            `json:"consumers"`
	Factors       gapi.Factors          `json:"factors"`
	Embodied      report.EmbodiedParams `json:"embodied"`
	Idle          map[string]float64    `json:"idle_w"`
	Benchmarks    []bench.Result        `json:"benchmarks"`
	Events        []store.ContextEvent  `json:"events"`
	Regression    analysisParams        `json:"regression"`
}

type analysisParams struct {
	BaselineDays       int     `json:"baseline_days"`
	MinBaselineDays    int     `json:"min_baseline_days"`
	ZThreshold         float64 `json:"z_threshold"`
	MinGeneratedTokens int     `json:"min_generated_tokens"`
}

func round(v float64, digits int) float64 {
	p := math.Pow(10, float64(digits))
	return math.Round(v*p) / p
}

// activity is the load of the workspace at a local time, from 0 to 1: busy
// office hours, quiet nights and weekends.
func activity(t time.Time) float64 {
	h := float64(t.Hour()) + float64(t.Minute())/60
	var a float64
	switch {
	case h >= 9 && h < 19:
		a = 0.65 + 0.3*math.Sin((h-9)/10*math.Pi)
	case h >= 7 && h < 9, h >= 19 && h < 23:
		a = 0.3
	default:
		a = 0.06
	}
	if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
		a *= 0.35
	}
	return a
}

func simulate(end time.Time, loc *time.Location) Dataset {
	// A fixed seed gives the same workspace on every build; nothing here is
	// secret.
	rng := rand.New(rand.NewPCG(20261021, 7)) //nolint:gosec // reproducible simulation, not security
	hours := days * 24
	n := hours + ahead
	ds := Dataset{
		SchemaVersion: 1, End: end, Hours: hours, Ahead: ahead,
		Domains:   map[string][]float64{"package": make([]float64, n), "dram": make([]float64, n), "gpu": make([]float64, n)},
		Consumers: []Consumer{{"chat-app", 0.52}, {"search-api", 0.31}, {"batch-eval", 0.17}},
		Factors: gapi.Factors{EmissionGPerKWh: indicators.DefaultEmissionFactor, EmissionSource: indicators.DefaultEmissionSource,
			PUE: 1, Currency: "MXN", TimeZone: timeZone},
		Idle:       map[string]float64{"package": 38, "dram": 4.5, "gpu": 22},
		Regression: analysisParams{BaselineDays: 14, MinBaselineDays: 7, ZThreshold: 3, MinGeneratedTokens: 1000},
	}
	server, gpu, years := 1200.0, 320.0, 4.0
	ds.Embodied = report.EmbodiedParams{ServerKgCO2e: &server, GPUKgCO2e: &gpu, LifetimeYears: &years}
	for _, m := range models {
		ds.Models = append(ds.Models, ModelData{Backend: m.backend, Kind: m.kind, Endpoint: m.endpoint, Model: m.name,
			EnergyJ: make([]float64, n), GPUJ: make([]float64, n), Generated: make([]float64, n),
			Prompt: make([]float64, n), Requests: make([]float64, n)})
	}
	for h := range n {
		t := end.Add(time.Duration(h-hours) * time.Hour).In(loc)
		act := activity(t.Add(30 * time.Minute))
		var cpuJ, gpuJ float64
		for i, m := range models {
			gen := math.Round(m.rate * act * (0.85 + 0.3*rng.Float64()))
			jpt := m.jpt * (0.96 + 0.08*rng.Float64())
			if m.worseFrom > 0 && h >= m.worseFrom*24 {
				jpt *= m.worse
			}
			e := gen * jpt
			d := &ds.Models[i]
			d.Generated[h], d.EnergyJ[h], d.GPUJ[h] = gen, math.Round(e), math.Round(e*m.gpu)
			d.Requests[h] = math.Round(gen / 210)
			d.Prompt[h] = d.Requests[h] * 480
			cpuJ += e * (1 - m.gpu)
			gpuJ += e * m.gpu
		}
		ds.Domains["package"][h] = round(ds.Idle["package"]+cpuJ*0.85/3600+6*act+rng.Float64(), 1)
		ds.Domains["dram"][h] = round(ds.Idle["dram"]+cpuJ*0.15/3600+0.3*rng.Float64(), 1)
		ds.Domains["gpu"][h] = round(ds.Idle["gpu"]+gpuJ/3600+0.8*rng.Float64(), 1)
	}
	digest := func() string { return fmt.Sprintf("%016x%016x", rng.Uint64(), rng.Uint64()) }
	old, changed := digest(), digest()
	at := func(day, hour int) time.Time {
		t := end.Add(time.Duration(day*24-hours) * time.Hour).In(loc)
		return time.Date(t.Year(), t.Month(), t.Day(), hour, 0, 0, 0, loc).UTC()
	}
	ds.Events = []store.ContextEvent{
		{ID: 1, At: at(0, 0), Kind: store.ContextAgentVersion, Value: "demo"},
		{ID: 2, At: at(0, 0), Kind: store.ContextModelDigest, Subject: "ollama-11434/llama3.2:3b", Value: old},
		{ID: 3, At: at(days-10, 15), Kind: store.ContextConfig, Value: digest()[:16], Previous: digest()[:16]},
		{ID: 4, At: at(days-4, 8), Kind: store.ContextModelDigest, Subject: "ollama-11434/llama3.2:3b", Value: changed, Previous: old},
	}
	ds.Benchmarks = benchmarks(end, rng)
	return ds
}

func benchmarks(end time.Time, rng *rand.Rand) []bench.Result {
	var out []bench.Result
	for i, m := range []model{models[0], models[1], models[4]} {
		start := end.Add(-time.Duration(30-i*9) * 24 * time.Hour).Add(-time.Duration(3+i) * time.Hour)
		r := bench.Result{
			ID: int64(i + 1), StartedAt: start, EndedAt: start.Add(9 * time.Minute),
			Target:          bench.Target{Kind: discovery.Kind(m.kind), Endpoint: m.endpoint, Model: m.name},
			Params:          bench.Params{MaxTokens: 256, Temperature: 0, Seed: 42, Runs: 5, WarmupS: 30, MinRunS: 60, Passes: 3},
			PromptSetSHA256: strings.Repeat("ab", 32), Prompts: 8,
			Context: bench.Context{CPU: "Simulated 16-core processor", GPUs: []string{"Simulated 24 GB GPU"}, Governor: "performance",
				OtherCPUShare: 0.03, Channels: []string{"rapl:package-0", "rapl:dram-0", "nvml:0000:01:00.0"}},
			Basis: bench.BasisBackend, Calibrated: true,
		}
		var jpts, energies []float64
		for range 5 {
			gen := int64(5000 + rng.IntN(500))
			jpt := m.jpt * 0.95 * (0.985 + 0.03*rng.Float64())
			e := float64(gen) * jpt
			r.Runs = append(r.Runs, bench.Run{EnergyJ: e, BackendJ: e, DurationS: 62 + 4*rng.Float64(), PromptTokens: 3840, GeneratedTokens: gen, JPerToken: jpt})
			r.GeneratedTokens += gen
			jpts, energies = append(jpts, jpt), append(energies, e)
		}
		r.JPerToken, _ = stats.Summarize(jpts)
		r.Energy, _ = stats.Summarize(energies)
		out = append(out, r)
	}
	return out
}

// period sums the dataset in [from, to) by whole hours.
func period(ds Dataset, from, to time.Time) (map[string]float64, []store.UsageEnergy) {
	first := ds.Hours - int(ds.End.Sub(from).Hours())
	last := ds.Hours - int(ds.End.Sub(to).Hours())
	domains := map[string]float64{}
	for d, w := range ds.Domains {
		for h := max(first, 0); h < min(last, ds.Hours); h++ {
			domains[d] += w[h] * 3600
		}
	}
	var rows []store.UsageEnergy
	for _, m := range ds.Models {
		u := store.UsageEnergy{Backend: m.Backend, Kind: m.Kind, Model: m.Model, Counted: true}
		for h := max(first, 0); h < min(last, ds.Hours); h++ {
			u.EnergyJ += m.EnergyJ[h]
			u.GPUEnergyJ += m.GPUJ[h]
			u.GeneratedTokens += int64(m.Generated[h])
			u.PromptTokens += int64(m.Prompt[h])
			u.Requests += int64(m.Requests[h])
		}
		rows = append(rows, u)
	}
	return domains, rows
}

func withIndicators(f indicators.Factors, rows []store.UsageEnergy) []store.UsageEnergy {
	for i := range rows {
		v := f.Compute(rows[i].EnergyJ)
		rows[i].Indicators = &v
	}
	return rows
}

// reports renders the environmental and SCI reports of the last seven days
// of the dataset, in every language and format.
func reports(out string, ds Dataset) error {
	to := ds.End
	from := to.Add(-7 * 24 * time.Hour)
	f := indicators.Default()
	domains, modelRows := period(ds, from, to)
	modelRows = withIndicators(f, modelRows)
	var backends []store.UsageEnergy
	for _, m := range modelRows {
		found := false
		for i := range backends {
			if backends[i].Backend == m.Backend {
				backends[i].EnergyJ += m.EnergyJ
				backends[i].GPUEnergyJ += m.GPUEnergyJ
				backends[i].GeneratedTokens += m.GeneratedTokens
				backends[i].Requests += m.Requests
				found = true
			}
		}
		if !found {
			backends = append(backends, store.UsageEnergy{Backend: m.Backend, Kind: m.Kind, Counted: true, EnergyJ: m.EnergyJ,
				GPUEnergyJ: m.GPUEnergyJ, GeneratedTokens: m.GeneratedTokens, Requests: m.Requests})
		}
	}
	backends = withIndicators(f, backends)
	var chans []store.ChannelEnergy
	node := report.Node{}
	for _, d := range []struct {
		domain  source.Domain
		channel string
	}{{source.DomainPackage, "rapl:package-0"}, {source.DomainDRAM, "rapl:dram-0"}, {source.DomainGPU, "nvml:0000:01:00.0"}} {
		j := domains[string(d.domain)]
		v := f.Compute(j)
		chans = append(chans, store.ChannelEnergy{BucketStart: from, Channel: d.channel, Domain: d.domain, Quality: "measured", EnergyJ: j, Indicators: &v})
		if d.domain == source.DomainGPU {
			node.GPUJ += j
		} else {
			node.ServerJ += j
		}
		node.Indicators.Add(v)
	}
	for _, m := range modelRows {
		node.GeneratedTokens += m.GeneratedTokens
		node.Requests += m.Requests
	}
	e := indicators.Embodied{ServerKg: ds.Embodied.ServerKgCO2e, GPUKg: ds.Embodied.GPUKgCO2e, LifetimeYears: ds.Embodied.LifetimeYears}
	sciIn := report.SCIInput{From: from, To: to, Unit: indicators.UnitGeneratedTokens, Factors: ds.Factors, Embodied: e, Node: node}
	nodeSCI := report.BuildSCI(sciIn)
	sciIn.GroupBy, sciIn.Groups = store.ByModel, modelRows
	sci := report.BuildSCI(sciIn)
	esg := report.BuildESG(report.ESGInput{From: from, To: to, Now: to, Version: "demo", Factors: ds.Factors,
		Channels: chans, Backends: backends, Models: modelRows, SCI: nodeSCI})
	dir := filepath.Join(out, "reports")
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // public pages of the site
		return err
	}
	for _, lang := range i18n.Supported {
		l, err := i18n.New(lang)
		if err != nil {
			return err
		}
		// Every page says that its figures are simulated.
		mark := func(b []byte) []byte {
			return bytes.Replace(b, []byte("<main>"), []byte("<main>\n<p class=\"muted\"><strong>"+l.T("gui.demo.banner")+"</strong></p>"), 1)
		}
		var b bytes.Buffer
		if err := report.RenderESG(&b, esg, l); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "esg-"+lang+".html"), mark(b.Bytes()), 0o644); err != nil { //nolint:gosec // public page of the site
			return err
		}
		b.Reset()
		if err := report.RenderSCI(&b, sci, to, "demo", l); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "sci-"+lang+".html"), mark(b.Bytes()), 0o644); err != nil { //nolint:gosec // public page of the site
			return err
		}
	}
	for name, doc := range map[string]any{"esg.json": esg, "sci.json": sci} {
		if err := writeJSON(filepath.Join(dir, name), doc); err != nil {
			return err
		}
	}
	var b bytes.Buffer
	if err := report.WriteESGCSV(&b, esg); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "esg.csv"), b.Bytes(), 0o644); err != nil { //nolint:gosec // public file of the site
		return err
	}
	b.Reset()
	if err := report.WriteSCICSV(&b, sci); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "sci.csv"), b.Bytes(), 0o644) //nolint:gosec // public file of the site
}

func writeJSON(p string, v any) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { //nolint:gosec // public files of the site
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644) //nolint:gosec // public file of the site
}

// docs writes the catalogs, the help and the about document, as the agent
// serves them.
func docs(out string) error {
	d, err := agent.Docs()
	if err != nil {
		return err
	}
	for _, lang := range i18n.Supported {
		cat, err := agent.GUICatalog(lang)
		if err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(out, "data", "catalog-"+lang+".json"), cat); err != nil {
			return err
		}
		idx := gapi.HelpIndex{SchemaVersion: gapi.SchemaVersion, Lang: lang, Sections: helpSections(), Pages: d.Index(lang)}
		if err := writeJSON(filepath.Join(out, "data", "help", lang, "index.json"), idx); err != nil {
			return err
		}
		for _, p := range idx.Pages {
			title, html, ok := d.Render(lang, p.Path)
			if !ok {
				return fmt.Errorf("help page %s/%s", lang, p.Path)
			}
			name := p.Path
			if name == "" {
				name = "_index"
			}
			page := gapi.HelpPage{SchemaVersion: gapi.SchemaVersion, Lang: lang, Path: p.Path, Title: title, HTML: html}
			if err := writeJSON(filepath.Join(out, "data", "help", lang, name+".json"), page); err != nil {
				return err
			}
		}
	}
	return writeJSON(filepath.Join(out, "data", "about.json"), about.Get())
}

func helpSections() []string { return []string{"tutorials", "guides", "reference", "explanation"} }

// interfaceFiles copies the compiled interface, without its service
// worker, and loads the demo script before it.
func interfaceFiles(out string) error {
	dist := web.Assets()
	err := fs.WalkDir(dist, ".", func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || p == "sw.js" {
			return err
		}
		b, err := fs.ReadFile(dist, p)
		if err != nil {
			return err
		}
		if p == "index.html" {
			if !bytes.Contains(b, []byte(`<script type="module" src="main.js"></script>`)) {
				return errors.New("index.html does not load main.js as expected")
			}
			b = bytes.Replace(b, []byte(`<script type="module" src="main.js"></script>`),
				[]byte(`<script src="demo.js"></script>`+"\n    "+`<script type="module" src="main.js"></script>`), 1)
		}
		dst := filepath.Join(out, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // public files of the site
			return err
		}
		return os.WriteFile(dst, b, 0o644) //nolint:gosec // public file of the site
	})
	if err != nil {
		return err
	}
	res := api.Build(api.BuildOptions{
		EntryPoints: []string{filepath.Join("web", "src", "demo", "demo.ts")}, Outfile: filepath.Join(out, "demo.js"),
		Bundle: true, Write: true, Format: api.FormatIIFE, Target: api.ES2022, Platform: api.PlatformBrowser,
		MinifyWhitespace: true, MinifyIdentifiers: true, MinifySyntax: true, TsconfigRaw: "{}",
		Charset: api.CharsetUTF8, LegalComments: api.LegalCommentsNone, Banner: map[string]string{"js": jsBanner},
		LogLevel: api.LogLevelSilent,
	})
	if len(res.Errors) > 0 {
		return errors.New(strings.Join(api.FormatMessages(res.Errors, api.FormatMessagesOptions{Kind: api.ErrorMessage}), ""))
	}
	return nil
}

func run(out string) error {
	loc, err := time.LoadLocation(timeZone)
	if err != nil {
		return err
	}
	if err := clean(out); err != nil {
		return err
	}
	ds := simulate(time.Now().UTC().Truncate(time.Hour), loc)
	for _, step := range []func() error{
		func() error { return interfaceFiles(out) },
		func() error { return docs(out) },
		func() error { return reports(out, ds) },
		func() error { return writeJSON(filepath.Join(out, "data", "workspace.json"), ds) },
	} {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// clean empties the output directory of an earlier run. Any other
// directory with files in it is left alone.
func clean(out string) error {
	entries, err := os.ReadDir(out)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && len(entries) == 0) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(out, "demo.js")); err != nil {
		return fmt.Errorf("%s is not empty and holds no demo; choose another directory", out)
	}
	return os.RemoveAll(out)
}

func main() {
	out := flag.String("out", path.Join("site", "static", "demo"), "output directory")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "gen-site-demo:", err)
		os.Exit(1)
	}
}
