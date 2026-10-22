// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/agent"
	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/bench"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/report"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/ipmi"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/perfevent"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/registry"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/sourcetest"
)

type counter struct{}

func (counter) Read() (uint64, error) { return 1 << 32, nil }
func (counter) Close() error          { return nil }

type perfOK struct{}

func (perfOK) Open(uint32, uint64, int) (perfevent.Counter, error) { return counter{}, nil }

type noIPMI struct{}

func (noIPMI) Open(string) (ipmi.Device, error) { return nil, fs.ErrNotExist }

// fixtureHooks reads a recorded host instead of this machine.
func fixtureHooks(t *testing.T, host string) hooks {
	fsys := sourcetest.Host(t, host)
	return hooks{
		hostFS: fsys,
		sources: func(config.Config) []source.Source {
			return registry.Sources(registry.Options{FS: fsys, Perf: perfOK{}, IPMI: noIPMI{}})
		},
	}
}

// rising is a counter source that grows by 5 J at every read.
type rising struct {
	mu sync.Mutex
	j  float64
}

func (r *rising) ID() string    { return "rapl" }
func (r *rising) Priority() int { return 1 }
func (r *rising) Close() error  { return nil }
func (r *rising) Probe(context.Context) (source.ProbeResult, error) {
	return source.ProbeResult{Source: "rapl", Channels: []source.Channel{{
		ID: "rapl:package-0", Source: "rapl", Domain: source.DomainPackage, Instance: "0",
		Quality: source.QualityMeasured, Method: source.MethodMeasured, Kind: source.KindCounter,
	}}}, nil
}
func (r *rising) Read(context.Context) ([]source.Reading, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.j += 5
	return []source.Reading{{Channel: "rapl:package-0", EnergyJ: r.j}}, nil
}

func risingHooks() hooks {
	return hooks{sources: func(config.Config) []source.Source { return []source.Source{&rising{}} }}
}

func runWith(t *testing.T, h hooks, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := execute(context.Background(), args, &out, &errOut, func(k string) string { return env[k] }, h)
	return code, out.String(), errOut.String()
}

func TestDoctorOnRecordedHosts(t *testing.T) {
	for _, tt := range []struct {
		host, selected string
		estimated      bool
	}{
		{"amd-zen4-desktop", "rapl:package-0", false},
		{"vm-no-sensors", "estimate:cpu", true},
		{"arm-ampere-altra", "hwmon:package-0", false},
	} {
		t.Run(tt.host, func(t *testing.T) {
			code, out, errOut := runWith(t, fixtureHooks(t, tt.host), nil, "doctor", "--output", "json")
			if code != 0 {
				t.Fatalf("exit %d: %s", code, errOut)
			}
			var rep DoctorReport
			if err := json.Unmarshal([]byte(out), &rep); err != nil {
				t.Fatal(err)
			}
			if rep.Estimated != tt.estimated || !slices.ContainsFunc(rep.Channels, func(c api.Channel) bool { return c.ID == tt.selected && c.Selected }) {
				t.Fatalf("report %+v", rep)
			}
		})
	}
	code, out, _ := runWith(t, fixtureHooks(t, "amd-zen4-desktop"), map[string]string{"GREENLENS_LANG": "es"}, "doctor")
	for _, want := range []string{"Entorno", "Fuentes de energía", "rapl:package-0", "se usa", "rapl ya mide este dominio", "AMD Ryzen 9 7900X"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("doctor in Spanish lacks %q:\n%s", want, out)
		}
	}
}

func TestDoctorReportsABrokenConfiguration(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("agent:\n  bogus: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runWith(t, fixtureHooks(t, "amd-zen4-desktop"), nil, "doctor", "--config", p)
	if code != 0 || !strings.Contains(out, "GL3002") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestConfigCommands(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.yaml")
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(good, []byte("logging:\n  level: debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("logging:\n  level: loud\n  format: xml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runWith(t, hooks{}, nil, "config", "validate", "--config", good); code != 0 || !strings.Contains(out, "valid") {
		t.Errorf("validate good: %d %s", code, out)
	}
	code, _, errOut := runWith(t, hooks{}, nil, "config", "validate", "--config", bad)
	if code != 6 || strings.Count(errOut, "GL3003") != 2 {
		t.Errorf("validate bad: exit %d\n%s", code, errOut)
	}
	if code, out, _ := runWith(t, hooks{}, nil, "config", "show", "--config", good); code != 0 || !strings.Contains(out, "level: debug") || !strings.Contains(out, "sample_interval: 1s") {
		t.Errorf("show: %d %s", code, out)
	}
	if code, out, _ := runWith(t, hooks{}, nil, "config", "path", "--config", good, "-o", "json"); code != 0 || !strings.Contains(out, `"path": "`+good+`"`) {
		t.Errorf("path: %d %s", code, out)
	}
	if code, _, errOut := runWith(t, hooks{}, nil, "config", "nonsense"); code != 2 || !strings.Contains(errOut, "GL0002") {
		t.Errorf("unknown subcommand: %d %s", code, errOut)
	}
}

// fakeSystem records the changes instead of making them.
type fakeSystem struct {
	root   bool
	group  bool
	files  map[string]string
	ran    [][]string
	failAt string
}

func (f *fakeSystem) euid() int {
	if f.root {
		return 0
	}
	return 1000
}
func (f *fakeSystem) groupExists(string) bool { return f.group }
func (f *fakeSystem) writeFile(p, c string) error {
	if f.files == nil {
		f.files = map[string]string{}
	}
	f.files[p] = c
	return nil
}
func (f *fakeSystem) run(_ context.Context, argv []string) error {
	f.ran = append(f.ran, argv)
	if argv[0] == f.failAt {
		return os.ErrPermission
	}
	return nil
}

func TestSetupPermissions(t *testing.T) {
	sys := &fakeSystem{}
	code, out, _ := runWith(t, hooks{system: sys}, nil, "setup-permissions", "--ipmi", "--user", "alice")
	if code != 0 || len(sys.ran) != 0 || len(sys.files) != 0 || !strings.Contains(out, "groupadd --system greenlens-power") ||
		!strings.Contains(out, "usermod -aG greenlens-power alice") || !strings.Contains(out, "sudo greenlens setup-permissions --apply") {
		t.Fatalf("plan only: exit %d, ran %v\n%s", code, sys.ran, out)
	}
	if code, _, errOut := runWith(t, hooks{system: sys}, nil, "setup-permissions", "--apply"); code != 5 || !strings.Contains(errOut, "GL1011") {
		t.Fatalf("apply without root: %d %s", code, errOut)
	}
	sys = &fakeSystem{root: true, group: true}
	code, _, errOut := runWith(t, hooks{system: sys}, nil, "setup-permissions", "--apply", "--yes", "--ipmi")
	if code != 0 {
		t.Fatalf("apply: %d %s", code, errOut)
	}
	if len(sys.files) != 2 || sys.ran[0][0] != "udevadm" || !slices.Contains(sys.ran[1], "--subsystem-match=ipmi") {
		t.Fatalf("files %v ran %v", sys.files, sys.ran)
	}
	for path, deployed := range map[string]string{raplRulePath: "70-greenlens-power.rules", ipmiRulePath: "71-greenlens-ipmi.rules"} {
		want, err := os.ReadFile(filepath.Join("..", "..", "deploy", "udev", deployed))
		if err != nil {
			t.Fatal(err)
		}
		if sys.files[path] != string(want) {
			t.Errorf("%s differs from deploy/udev/%s", path, deployed)
		}
	}
	sys = &fakeSystem{root: true, group: true, failAt: "udevadm"}
	if code, _, errOut := runWith(t, hooks{system: sys}, nil, "setup-permissions", "--apply", "--yes"); code != 5 || !strings.Contains(errOut, "GL1012") {
		t.Fatalf("failing step: %d %s", code, errOut)
	}
	if code, _, errOut := runWith(t, hooks{system: &fakeSystem{}}, nil, "setup-permissions", "--user", "Bad Name"); code != 2 || !strings.Contains(errOut, "GL0005") {
		t.Fatalf("bad user: %d %s", code, errOut)
	}
}

func TestMeasureCommand(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "r.json")
	env := map[string]string{"HOME": dir, "PATH": os.Getenv("PATH")}
	code, _, errOut := runWith(t, risingHooks(), env, "measure", "--interval", "20ms", "--report", report, "-o", "json", "--", "sh", "-c", "sleep 0.2; exit 3")
	if code != 3 {
		t.Fatalf("exit code %d, want the 3 of the command\n%s", code, errOut)
	}
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var rep MeasureReport
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.TargetKind != "command" || rep.ExitCode == nil || *rep.ExitCode != 3 || rep.NodeJ[source.DomainPackage] <= 0 ||
		rep.DurationS < 0.2 || rep.Quality != source.QualityMeasured || rep.Indicators.KWh < 0 || rep.BaselineSource != "none" {
		t.Fatalf("report %+v", rep)
	}
	if rep.TotalJ > rep.NodeJ[source.DomainPackage]+1e-9 {
		t.Errorf("attributed %v exceeds the node %v", rep.TotalJ, rep.NodeJ[source.DomainPackage])
	}
}

func TestMeasureTargets(t *testing.T) {
	h := risingHooks()
	for _, tt := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"measure"}, 2, "GL0012"},
		{[]string{"measure", "--pid", "1", "--", "true"}, 2, "GL0012"},
		{[]string{"measure", "stray"}, 2, "GL0007"},
		{[]string{"measure", "--pid", "999999999"}, 1, "GL0013"},
		{[]string{"measure", "--", "no-such-command-greenlens"}, 1, "GL0011"},
	} {
		code, _, errOut := runWith(t, h, map[string]string{"PATH": os.Getenv("PATH")}, tt.args...)
		if code != tt.code || !strings.Contains(errOut, tt.want) {
			t.Errorf("%v: exit %d, stderr %s", tt.args, code, errOut)
		}
	}
	h = fixtureHooks(t, "amd-zen4-desktop")
	if code, _, errOut := runWith(t, h, nil, "measure", "--unit", "ollama"); code != 1 || !strings.Contains(errOut, "GL0014") || !strings.Contains(errOut, "ollama.service") {
		t.Errorf("missing unit: %d %s", code, errOut)
	}
}

// fakeOllama generates 10 tokens for each prompt of a known model.
func fakeOllama(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.Model != "qwen2.5:0.5b" || r.URL.Path != "/api/generate" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"model not found"}`)
			return
		}
		time.Sleep(5 * time.Millisecond)
		_, _ = io.WriteString(w, `{"model":"qwen2.5:0.5b","response":"x","done":true,"prompt_eval_count":20,"eval_count":10}`)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestBenchCommand(t *testing.T) {
	url := fakeOllama(t)
	env := map[string]string{"HOME": t.TempDir()}
	code, out, errOut := runWith(t, risingHooks(), env, "bench", "--endpoint", url, "--model", "qwen2.5:0.5b", "--runs", "3", "--warmup", "0s", "--min-run", "0s", "--interval", "50ms", "--no-save", "-o", "json")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	var r bench.Result
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Runs) != 3 || r.GeneratedTokens != 3*80 || r.Basis != bench.BasisTotal || r.Runs[0].EnergyJ <= 0 || r.JPerToken.Mean <= 0 || r.PromptSetSHA256 != bench.Hash(bench.DefaultPrompts()) {
		t.Fatalf("result %+v", r)
	}
	for _, tt := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"bench"}, 2, "GL0005"},
		{[]string{"bench", "--model", "m", "--kind", "process"}, 2, "GL0005"},
		{[]string{"bench", "--model", "m", "--runs", "1"}, 2, "GL0005"},
		{[]string{"bench", "--model", "m", "--endpoint", "ftp://x"}, 2, "GL0005"},
		{[]string{"bench", "--endpoint", url, "--model", "missing", "--warmup", "0s", "--min-run", "0s", "--no-save"}, 4, "GL2003"},
		{[]string{"compare", "1"}, 2, "GL0015"},
	} {
		code, _, errOut := runWith(t, risingHooks(), env, tt.args...)
		if code != tt.code || !strings.Contains(errOut, tt.want) {
			t.Errorf("%v: exit %d, stderr %s", tt.args, code, errOut)
		}
	}
}

func TestCalibrateRefusesAnEstimate(t *testing.T) {
	code, _, errOut := runWith(t, fixtureHooks(t, "vm-no-sensors"), nil, "calibrate")
	if code != 3 || !strings.Contains(errOut, "GL1001") {
		t.Fatalf("exit %d %s", code, errOut)
	}
	if code, _, errOut := runWith(t, risingHooks(), nil, "calibrate", "--duration", "1s"); code != 2 || !strings.Contains(errOut, "GL0005") {
		t.Fatalf("short duration: %d %s", code, errOut)
	}
}

// TestCommandsAgainstAnAgent runs status and report against a live agent.
func TestCommandsAgainstAnAgent(t *testing.T) {
	dir := t.TempDir()
	sockDir, err := os.MkdirTemp("", "gl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(sockDir) }()
	cfgPath := filepath.Join(dir, "greenlens.yaml")
	cfgText := "agent:\n  sample_interval: 50ms\n  data_dir: " + filepath.Join(dir, "data") + "\n  socket: " + filepath.Join(sockDir, "a.sock") + "\nserver:\n  enabled: false\n"
	if err := os.WriteFile(cfgPath, []byte(cfgText), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(cfgPath, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		done <- agent.Run(ctx, agent.Options{Config: cfg, ConfigPath: cfgPath, Getenv: func(string) string { return "" },
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Sources: []source.Source{&rising{}},
			HostFS: sourcetest.Host(t, "amd-zen4-desktop"), NoSandbox: true, Ready: ready, Version: "test"})
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	<-ready
	time.Sleep(300 * time.Millisecond)

	code, out, errOut := runWith(t, hooks{}, nil, "status", "--config", cfgPath)
	if code != 0 || !strings.Contains(out, "CPU package") || !strings.Contains(out, "greenlens calibrate") {
		t.Fatalf("status: %d\n%s%s", code, out, errOut)
	}
	code, out, _ = runWith(t, hooks{}, nil, "status", "--config", cfgPath, "-o", "json")
	if code != 0 || !strings.Contains(out, `"schema_version": 1`) {
		t.Fatalf("status json: %d %s", code, out)
	}
	code, out, errOut = runWith(t, hooks{}, nil, "report", "--config", cfgPath, "--from", "1h", "-o", "json")
	var rep Report
	if code != 0 || json.Unmarshal([]byte(out), &rep) != nil || rep.Total.ComponentsJ <= 0 || rep.Resolution != "raw" || rep.EmissionGKWh != 444 {
		t.Fatalf("report: %d %+v\n%s", code, rep, errOut)
	}
	code, out, _ = runWith(t, hooks{}, map[string]string{"GREENLENS_LANG": "es"}, "--config", cfgPath)
	if code != 0 || !strings.Contains(out, "El agente está en ejecución") {
		t.Fatalf("brief: %d %s", code, out)
	}
	if code, _, errOut := runWith(t, hooks{}, nil, "report", "--config", cfgPath, "--by", "week"); code != 2 || !strings.Contains(errOut, "GL0005") {
		t.Fatalf("bad --by: %d %s", code, errOut)
	}
	// The recorded host runs no inference server.
	if code, out, _ := runWith(t, hooks{}, nil, "status", "--config", cfgPath); code != 0 || !strings.Contains(out, "No inference backend found") {
		t.Fatalf("status without backends: %d\n%s", code, out)
	}
	code, out, errOut = runWith(t, hooks{}, nil, "report", "--config", cfgPath, "--group", "model", "-o", "json")
	var brep BackendReport
	if code != 0 || json.Unmarshal([]byte(out), &brep) != nil || brep.GroupBy != "model" || brep.Rows == nil || len(brep.Rows) != 0 {
		t.Fatalf("report by model: %d %+v\n%s", code, brep, errOut)
	}
	if code, out, _ := runWith(t, hooks{}, map[string]string{"GREENLENS_LANG": "es"}, "report", "--config", cfgPath, "--group", "backend"); code != 0 || !strings.Contains(out, "no se atribuyó energía") {
		t.Fatalf("report by backend: %d %s", code, out)
	}
	// SCI of the node, without embodied carbon configured.
	code, out, errOut = runWith(t, hooks{}, nil, "sci", "--config", cfgPath, "--from", "1h", "-o", "json")
	var sci report.SCIReport
	if code != 0 || json.Unmarshal([]byte(out), &sci) != nil || sci.Node.EnergyJ <= 0 || sci.Node.SCI.OperationalG <= 0 ||
		sci.Unit != "1k_generated_tokens" || len(sci.Missing) != 3 || sci.Node.SCI.EmbodiedG != nil {
		t.Fatalf("sci: %d %+v %s", code, sci, errOut)
	}
	if code, out, _ := runWith(t, hooks{}, map[string]string{"GREENLENS_LANG": "es"}, "sci", "--config", cfgPath, "--group", "model", "--unit", "request"); code != 0 ||
		!strings.Contains(out, "No se incluye el carbono incorporado") || !strings.Contains(out, "por solicitud") {
		t.Fatalf("sci table: %d %s", code, out)
	}
	for _, args := range [][]string{{"--unit", "watt"}, {"--group", "team"}, {"--from", "soon"}} {
		if code, _, errOut := runWith(t, hooks{}, nil, append([]string{"sci", "--config", cfgPath}, args...)...); code != 2 || !strings.Contains(errOut, "GL0005") {
			t.Fatalf("sci %v: %d %s", args, code, errOut)
		}
	}
	if code, out, _ := runWith(t, hooks{}, nil, "report", "--config", cfgPath, "--from", "30d", "--regressions"); code != 0 ||
		!strings.Contains(out, "No regression in the period") || !strings.Contains(out, "Changes of context") {
		t.Fatalf("regressions: %d %s", code, out)
	}
	code, out, _ = runWith(t, hooks{}, nil, "report", "--config", cfgPath, "--from", "30d", "--regressions", "-o", "json")
	var rg api.Regressions
	if code != 0 || json.Unmarshal([]byte(out), &rg) != nil || rg.Params.BaselineDays != 14 || len(rg.Events) == 0 {
		t.Fatalf("regressions json: %d %s", code, out)
	}
	if code, _, errOut := runWith(t, hooks{}, nil, "report", "--config", cfgPath, "--regressions", "--group", "model"); code != 2 || !strings.Contains(errOut, "GL0005") {
		t.Fatalf("regressions with group: %d %s", code, errOut)
	}
	// The environmental report, on the terminal, in JSON and as a page.
	if code, out, _ := runWith(t, hooks{}, nil, "report", "--config", cfgPath, "--from", "1h", "--esg"); code != 0 ||
		!strings.Contains(out, "Scope 2 emissions, location-based") || !strings.Contains(out, "market-based method") {
		t.Fatalf("esg: %d %s", code, out)
	}
	code, out, _ = runWith(t, hooks{}, nil, "report", "--config", cfgPath, "--from", "1h", "--esg", "-o", "json")
	var esg report.ESGReport
	if code != 0 || json.Unmarshal([]byte(out), &esg) != nil || esg.ComponentsJ <= 0 || esg.Method != report.MethodScope2Location {
		t.Fatalf("esg json: %d %s", code, out)
	}
	page := filepath.Join(dir, "esg.html")
	if code, out, errOut := runWith(t, hooks{}, map[string]string{"GREENLENS_LANG": "es"}, "report", "--config", cfgPath, "--from", "1h", "--esg", "--html", page); code != 0 || !strings.Contains(out, page) {
		t.Fatalf("esg html: %d %s %s", code, out, errOut)
	}
	if b, err := os.ReadFile(page); err != nil || !strings.Contains(string(b), `<html lang="es">`) || !strings.Contains(string(b), "Reporte ambiental") {
		t.Fatalf("esg page: %v %.200s", err, b)
	}
	if code, _, errOut := runWith(t, hooks{}, nil, "sci", "--config", cfgPath, "--from", "1h", "--html", filepath.Join(dir, "none", "sci.html")); code != 1 || !strings.Contains(errOut, "GL0019") {
		t.Fatalf("sci html in a missing directory: %d %s", code, errOut)
	}
	if code, _, errOut := runWith(t, hooks{}, nil, "report", "--config", cfgPath, "--html", page); code != 2 || !strings.Contains(errOut, "GL0005") {
		t.Fatalf("html without esg: %d %s", code, errOut)
	}
	if code, out, _ := runWith(t, hooks{}, nil, "proxy", "--config", cfgPath); code != 0 || !strings.Contains(out, "proxy.enabled") {
		t.Fatalf("proxy off: %d %s", code, out)
	}
	// Two benchmarks saved in the agent, then compared.
	url := fakeOllama(t)
	var ids []string
	for range 2 {
		code, out, errOut := runWith(t, risingHooks(), nil, "bench", "--config", cfgPath, "--endpoint", url, "--model", "qwen2.5:0.5b", "--runs", "2", "--warmup", "0s", "--min-run", "0s", "--interval", "50ms", "-o", "json")
		var r bench.Result
		if code != 0 || json.Unmarshal([]byte(out), &r) != nil || r.ID == 0 {
			t.Fatalf("bench: %d %s %s", code, out, errOut)
		}
		ids = append(ids, strconv.FormatInt(r.ID, 10))
	}
	code, out, errOut = runWith(t, hooks{}, nil, "compare", "--config", cfgPath, ids[0], ids[1], "-o", "json")
	var cmp CompareReport
	if code != 0 || json.Unmarshal([]byte(out), &cmp) != nil || cmp.Tokens != 1000 || cmp.A.Kind != "benchmark" || cmp.A.JPerToken <= 0 || cmp.Ratio <= 0 {
		t.Fatalf("compare: %d %+v %s", code, cmp, errOut)
	}
	if code, out, _ := runWith(t, hooks{}, nil, "compare", "--config", cfgPath, ids[0], ids[1]); code != 0 || !strings.Contains(out, "1000 generated tokens") {
		t.Fatalf("compare table: %d %s", code, out)
	}
	if code, _, errOut := runWith(t, hooks{}, nil, "compare", "--config", cfgPath, "llama3.2", "qwen"); code != 2 || !strings.Contains(errOut, "GL0005") {
		t.Fatalf("unknown models: %d %s", code, errOut)
	}
	code, out, errOut = runWith(t, hooks{}, nil, "export", "energy", "--config", cfgPath, "--from", "1h", "--bucket", "1m")
	if lines := strings.Split(strings.TrimSpace(out), "\n"); code != 0 || lines[0] != "bucket_start,channel,source,domain,quality,energy_j,samples" || len(lines) < 2 {
		t.Fatalf("export energy: %d %s %s", code, out, errOut)
	}
	code, out, errOut = runWith(t, hooks{}, nil, "export", "usage", "--config", cfgPath, "--from", "1h", "--bucket", "10m", "--group", "backend", "-o", "json")
	var ue UsageExport
	if code != 0 || json.Unmarshal([]byte(out), &ue) != nil || ue.GroupBy != "backend" || ue.BucketMS != 600_000 || ue.Rows == nil {
		t.Fatalf("export usage: %d %s %s", code, out, errOut)
	}
	code, out, errOut = runWith(t, hooks{}, nil, "export", "benchmarks", "--config", cfgPath)
	if lines := strings.Split(strings.TrimSpace(out), "\n"); code != 0 || len(lines) != 3 || !strings.HasPrefix(lines[0], "id,started_at") {
		t.Fatalf("export benchmarks: %d %s %s", code, out, errOut)
	}
	code, out, errOut = runWith(t, hooks{}, nil, "export", "alerts", "--config", cfgPath)
	if lines := strings.Split(strings.TrimSpace(out), "\n"); code != 0 || lines[0] != "id,rule,state,expr,op,threshold,for_s,labels,value,started_at,fired_at,resolved_at" {
		t.Fatalf("export alerts: %d %s %s", code, out, errOut)
	}
	code, out, errOut = runWith(t, hooks{}, nil, "export", "alerts", "--config", cfgPath, "-o", "json")
	var ae AlertsExport
	if code != 0 || json.Unmarshal([]byte(out), &ae) != nil || ae.Active == nil || ae.History == nil {
		t.Fatalf("export alerts json: %d %s %s", code, out, errOut)
	}
	if code, out, _ := runWith(t, hooks{}, nil, "status", "--config", cfgPath); code != 0 || !strings.Contains(out, "No alert is pending or firing.") {
		t.Fatalf("status without alerts: %d %s", code, out)
	}
	for _, args := range [][]string{{"usage", "--bucket", "30s"}, {"usage", "--from", "30d", "--bucket", "1m"}, {"usage", "--group", "team"}, {"benchmarks", "--limit", "0"}, {"alerts", "--from", "later"}} {
		if code, _, errOut := runWith(t, hooks{}, nil, append([]string{"export", "--config", cfgPath}, args...)...); code != 2 || !strings.Contains(errOut, "GL0005") {
			t.Fatalf("export %v: %d %s", args, code, errOut)
		}
	}
	if code, _, errOut := runWith(t, hooks{}, nil, "export", "--config", cfgPath, "models"); code != 2 || !strings.Contains(errOut, "GL0002") {
		t.Fatalf("export of an unknown dataset: %d %s", code, errOut)
	}
	// Tokens go through the running agent, which audits them.
	code, out, errOut = runWith(t, hooks{}, nil, "token", "create", "--config", cfgPath, "--name", "ci", "--role", "operator", "-o", "json")
	var tok api.CreatedToken
	if code != 0 || json.Unmarshal([]byte(out), &tok) != nil || tok.Token.Role != "operator" {
		t.Fatalf("token through the agent: %d %s %s", code, out, errOut)
	}
	if code, out, _ := runWith(t, hooks{}, nil, "token", "list", "--config", cfgPath, "-o", "json"); code != 0 || !strings.Contains(out, `"name": "ci"`) {
		t.Fatalf("token list through the agent: %d %s", code, out)
	}
	if code, _, errOut := runWith(t, hooks{}, nil, "token", "revoke", "--config", cfgPath, "nobody"); code != 1 || !strings.Contains(errOut, "GL0018") {
		t.Fatalf("revoke of an unknown token: %d %s", code, errOut)
	}
	if code, _, errOut := runWith(t, hooks{}, nil, "token", "revoke", "--config", cfgPath, "ci"); code != 0 {
		t.Fatalf("revoke through the agent: %d %s", code, errOut)
	}
	// The agent makes the backup while it keeps measuring, and refuses to
	// have its database replaced.
	backup := filepath.Join(dir, "backup.db")
	code, out, errOut = runWith(t, hooks{}, nil, "backup", "--config", cfgPath, backup)
	if code != 0 || !strings.Contains(out, "Copied by the agent") {
		t.Fatalf("backup through the agent: %d %s %s", code, out, errOut)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "data"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "backup-") {
			t.Errorf("copy left in the data directory: %s", e.Name())
		}
	}
	if code, _, errOut := runWith(t, hooks{}, nil, "restore", "--config", cfgPath, backup); code != 1 || !strings.Contains(errOut, "GL4009") {
		t.Fatalf("restore while the agent runs: %d %s", code, errOut)
	}
	for _, args := range [][]string{{"--group", "team"}, {"--group", "model", "--by", "day"}} {
		if code, _, errOut := runWith(t, hooks{}, nil, append([]string{"report", "--config", cfgPath}, args...)...); code != 2 || !strings.Contains(errOut, "GL0005") {
			t.Fatalf("%v: %d %s", args, code, errOut)
		}
	}
}

func TestStatusWithoutAgent(t *testing.T) {
	code, _, errOut := runWith(t, hooks{}, nil, "status")
	if code != 1 || !strings.Contains(errOut, "GL4001") {
		t.Fatalf("exit %d %s", code, errOut)
	}
}

func TestParseTime(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{
		"now": now, "30m": now.Add(-30 * time.Minute), "24h": now.Add(-24 * time.Hour), "7d": now.Add(-7 * 24 * time.Hour),
		"2w": now.Add(-14 * 24 * time.Hour), "2026-10-01T00:00:00Z": time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	} {
		if got, err := parseTime(in, now); err != nil || !got.Equal(want) {
			t.Errorf("parseTime(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := parseTime("yesterday", now); err == nil {
		t.Error("free text accepted")
	}
}
