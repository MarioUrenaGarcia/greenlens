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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/agent"
	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
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
	cfgText := "agent:\n  sample_interval: 50ms\n  data_dir: " + filepath.Join(dir, "data") + "\n  socket: " + filepath.Join(sockDir, "a.sock") + "\n"
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
