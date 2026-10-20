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
