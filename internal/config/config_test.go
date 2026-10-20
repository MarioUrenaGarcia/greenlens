// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "greenlens.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultsAreValid(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestExampleMatchesDefaults(t *testing.T) {
	data, err := os.ReadFile("../../deploy/config/greenlens.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg := Defaults()
	if err := Parse(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	d := Defaults()
	if cfg.Agent != d.Agent || cfg.Attribution != d.Attribution || cfg.Retention != d.Retention || cfg.Logging != d.Logging ||
		cfg.Indicators.PUE != d.Indicators.PUE || cfg.Indicators.EmissionFactorGPerKWh != nil {
		t.Fatalf("example differs from the defaults:\n%+v\n%+v", cfg, d)
	}
	// Every key of the configuration appears in the example.
	for _, v := range EnvVars() {
		last := v.Key[strings.LastIndex(v.Key, ".")+1:]
		if !strings.Contains(string(data), last+":") {
			t.Errorf("%s is missing from the example", v.Key)
		}
	}
}

func TestLoadPrecedence(t *testing.T) {
	p := write(t, "agent:\n  sample_interval: 2s\nlogging:\n  level: warn\n")
	cfg, used, err := Load(p, env(map[string]string{"GREENLENS_LOGGING_LEVEL": "debug"}))
	if err != nil {
		t.Fatal(err)
	}
	if used != p || cfg.Agent.SampleInterval.D() != 2*time.Second || cfg.Logging.Level != "debug" || cfg.Indicators.PUE != 1 {
		t.Fatalf("cfg %+v from %s", cfg, used)
	}
}

func TestUnknownKeyIsAnError(t *testing.T) {
	_, _, err := Load(write(t, "agent:\n  sample_intreval: 2s\n"), env(nil))
	var e *help.Error
	if !errors.As(err, &e) || e.Code != help.GL3002 {
		t.Fatalf("err %v", err)
	}
	if !strings.Contains(err.Error(), "sample_intreval") {
		t.Errorf("message does not name the key: %v", err)
	}
}

func TestMissingExplicitFile(t *testing.T) {
	_, _, err := Load(filepath.Join(t.TempDir(), "none.yaml"), env(nil))
	if help.As(err).Code != help.GL3001 {
		t.Fatalf("err %v", err)
	}
}

func TestSearchPaths(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "greenlens", "greenlens.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("logging:\n  format: text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, used, err := Load("", env(map[string]string{"XDG_CONFIG_HOME": dir}))
	if err != nil {
		t.Fatal(err)
	}
	if _, sysErr := os.Stat("/etc/greenlens/greenlens.yaml"); sysErr == nil {
		t.Skip("a system configuration exists on this machine")
	}
	if used != p || cfg.Logging.Format != "text" {
		t.Fatalf("used %s cfg %+v", used, cfg.Logging)
	}
	if got := Paths(env(map[string]string{"HOME": "/home/u"})); got[1] != "/home/u/.config/greenlens/greenlens.yaml" {
		t.Errorf("paths %v", got)
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	cfg := Defaults()
	cfg.Agent.SampleInterval = Duration(10 * time.Millisecond)
	cfg.Attribution.IdlePolicy = "everything"
	cfg.Logging.Level = "loud"
	cfg.Sources.Disable = []string{"nvml"}
	ef := 300.0
	cfg.Indicators.EmissionFactorGPerKWh = &ef
	errs := help.Flatten(cfg.Validate())
	if len(errs) < 5 {
		t.Fatalf("got %d problems: %v", len(errs), errs)
	}
	for _, e := range errs {
		if help.As(e).Code != help.GL3003 {
			t.Errorf("not GL3003: %v", e)
		}
	}
}

func TestEnvTypes(t *testing.T) {
	cfg := Defaults()
	err := ApplyEnv(&cfg, env(map[string]string{
		"GREENLENS_AGENT_SAMPLE_INTERVAL":           "500ms",
		"GREENLENS_SOURCES_DISABLE":                 "battery, ipmi",
		"GREENLENS_SOURCES_FORCE":                   "package=perf_event",
		"GREENLENS_INDICATORS_TARIFF_PRICE_PER_KWH": "2.75",
		"GREENLENS_SOURCES_AUTO":                    "false",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.SampleInterval.D() != 500*time.Millisecond || !slices.Equal(cfg.Sources.Disable, []string{"battery", "ipmi"}) ||
		cfg.Sources.Force["package"] != "perf_event" || *cfg.Indicators.Tariff.PricePerKWh != 2.75 || cfg.Sources.Auto {
		t.Fatalf("cfg %+v", cfg)
	}
	err = ApplyEnv(&cfg, env(map[string]string{"GREENLENS_INDICATORS_PUE": "high", "GREENLENS_SOURCES_FORCE": "package"}))
	if errs := help.Flatten(err); len(errs) != 2 || help.As(errs[0]).Code != help.GL3004 {
		t.Fatalf("err %v", err)
	}
}

func TestSelectorOptions(t *testing.T) {
	cfg := Defaults()
	cfg.Sources.Auto = false
	cfg.Sources.Force = map[string]string{"package": "perf_event"}
	opt := cfg.SelectorOptions()
	if slices.Contains(opt.Disable, "perf_event") || !slices.Contains(opt.Disable, "rapl") || opt.Force[source.DomainPackage] != "perf_event" {
		t.Fatalf("options %+v", opt)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Sources.Force = nil
	if cfg.Validate() == nil {
		t.Error("auto off without forced sources accepted")
	}
}

func TestFactors(t *testing.T) {
	cfg := Defaults()
	if f := cfg.Factors(); f.EmissionGPerKWh != 444 || f.TariffPerKWh != nil {
		t.Fatalf("defaults %+v", f)
	}
	ef, tariff := 120.0, 3.1
	cfg.Indicators.EmissionFactorGPerKWh, cfg.Indicators.EmissionFactorSource = &ef, "utility 2026"
	cfg.Indicators.Tariff.PricePerKWh = &tariff
	if f := cfg.Factors(); f.EmissionGPerKWh != 120 || *f.TariffPerKWh != 3.1 || f.EmissionSource != "utility 2026" {
		t.Fatalf("configured %+v", f)
	}
}

func TestBackends(t *testing.T) {
	good := "backends:\n" +
		"  - name: vllm-main\n    kind: vllm\n    endpoint: http://127.0.0.1:8000\n" +
		"  - name: ollama.dev\n    kind: ollama\n    endpoint: https://[::1]:11434/\n" +
		"  - name: tgi-container\n    kind: tgi\n    process: text-generation-launcher\n" +
		"  - name: batch_job\n    kind: process\n    process: python3\n" +
		"discovery:\n  enabled: false\n  interval: 30s\n" +
		"attribution:\n  max_models_per_backend: 5\n"
	cfg := Defaults()
	if err := Parse([]byte(good), &cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Backends) != 4 || cfg.Backends[2].Process != "text-generation-launcher" || cfg.Discovery.Enabled || cfg.Discovery.Interval.D() != 30*time.Second || cfg.Attribution.MaxModelsPerBackend != 5 {
		t.Fatalf("%+v", cfg)
	}
	for _, tt := range []struct{ yaml, key string }{
		{"backends:\n  - name: Main\n    kind: vllm\n    endpoint: http://127.0.0.1:8000\n", "backends[0].name"},
		{"backends:\n  - name: a\n    kind: vllm\n    endpoint: http://127.0.0.1:1\n  - name: a\n    kind: vllm\n    endpoint: http://127.0.0.1:2\n", "backends[1].name"},
		{"backends:\n  - name: a\n    kind: triton\n    endpoint: http://127.0.0.1:1\n", "backends[0].kind"},
		{"backends:\n  - name: a\n    kind: vllm\n", "backends[0].endpoint"},
		{"backends:\n  - name: a\n    kind: vllm\n    endpoint: ftp://127.0.0.1:1\n", "backends[0].endpoint"},
		{"backends:\n  - name: a\n    kind: vllm\n    endpoint: http://user:secret@127.0.0.1:1\n", "backends[0].endpoint"},
		{"backends:\n  - name: a\n    kind: vllm\n    endpoint: http://127.0.0.1:1/v1\n", "backends[0].endpoint"},
		{"backends:\n  - name: a\n    kind: vllm\n    endpoint: http://127.0.0.1:1/?token=x\n", "backends[0].endpoint"},
		{"backends:\n  - name: a\n    kind: process\n", "backends[0].process"},
		{"backends:\n  - name: a\n    kind: process\n    process: /usr/bin/python3\n", "backends[0].process"},
		{"discovery:\n  interval: 100ms\n", "discovery.interval"},
		{"attribution:\n  max_models_per_backend: 0\n", "attribution.max_models_per_backend"},
	} {
		cfg := Defaults()
		if err := Parse([]byte(tt.yaml), &cfg); err != nil {
			t.Fatalf("%s: %v", tt.yaml, err)
		}
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), tt.key) {
			t.Errorf("%q: error %v, want one about %s", tt.yaml, err, tt.key)
		}
	}
}

func TestObjectListsHaveNoVariable(t *testing.T) {
	for _, v := range EnvVars() {
		if strings.HasPrefix(v.Key, "backends") {
			t.Errorf("%s has a variable", v.Key)
		}
	}
	cfg := Defaults()
	if err := ApplyEnv(&cfg, func(string) string { return "x" }); err == nil {
		t.Fatal("garbage in every variable must fail")
	}
	if err := ApplyEnv(&cfg, env(map[string]string{"GREENLENS_ATTRIBUTION_MAX_MODELS_PER_BACKEND": "7", "GREENLENS_DISCOVERY_ENABLED": "false"})); err != nil {
		t.Fatal(err)
	}
	if cfg.Attribution.MaxModelsPerBackend != 7 || cfg.Discovery.Enabled {
		t.Fatalf("%+v", cfg)
	}
	var keys []string
	for _, f := range Fields() {
		keys = append(keys, f.Key)
	}
	if !slices.Contains(keys, "backends[].endpoint") || !slices.Contains(keys, "backends") {
		t.Fatalf("fields %v", keys)
	}
}
