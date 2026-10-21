// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package config

import (
	"errors"
	"fmt"
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

func TestServer(t *testing.T) {
	for _, tt := range []struct {
		listen string
		want   bool
	}{
		{"127.0.0.1:10060", true}, {"localhost:10060", true}, {"[::1]:10060", true}, {"127.0.0.53:1", true},
		{":10060", false}, {"0.0.0.0:10060", false}, {"192.0.2.10:10060", false}, {"[::]:10060", false}, {"10060", false},
	} {
		if got := IsLoopback(tt.listen); got != tt.want {
			t.Errorf("IsLoopback(%q) = %v", tt.listen, got)
		}
	}
	cfg := Defaults()
	if !cfg.Server.Enabled || cfg.Server.Listen != DefaultListen {
		t.Fatalf("defaults %+v", cfg.Server)
	}
	cfg.Server.Listen = "192.0.2.10"
	cfg.Server.TokensFile = "tokens"
	cfg.Server.TLS.CertFile = "/etc/greenlens/cert.pem"
	errs := help.Flatten(cfg.Validate())
	var keys []string
	for _, e := range errs {
		for i := 0; i+1 < len(help.As(e).Args); i += 2 {
			if help.As(e).Args[i] == "key" {
				keys = append(keys, help.As(e).Args[i+1].(string))
			}
		}
	}
	for _, want := range []string{"server.listen", "server.tokens_file", "server.tls"} {
		if !slices.Contains(keys, want) {
			t.Errorf("%s not reported: %v", want, keys)
		}
	}
	cfg = Defaults()
	cfg.Server.Enabled = false
	cfg.Server.Listen = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a disabled server needs no address: %v", err)
	}
}

func TestEdit(t *testing.T) {
	data, err := os.ReadFile("../../deploy/config/greenlens.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	out, err := Edit(data, map[string]string{
		"indicators.tariff.price_per_kwh":      "2.5",
		"indicators.emission_factor_g_per_kwh": "300",
		"indicators.emission_factor_source":    "Utility notice: 2026",
		"sources.disable":                      "battery, ipmi",
		"sources.force":                        "package=rapl",
		"agent.sample_interval":                "2s",
		"server.metrics_auth":                  "true",
		"indicators.wue_l_per_kwh":             "",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The comments of the file survive the edit.
	for _, comment := range []string{"# Grid emission factor in gCO2e/kWh", "# Ask for a token on /metrics too."} {
		if !strings.Contains(string(out), comment) {
			t.Errorf("lost the comment %q:\n%s", comment, out)
		}
	}
	cfg := Defaults()
	if err := Parse(out, &cfg); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	v := Values(cfg)
	for k, want := range map[string]string{
		"indicators.tariff.price_per_kwh": "2.5", "indicators.emission_factor_g_per_kwh": "300",
		"indicators.emission_factor_source": "Utility notice: 2026", "sources.disable": "battery,ipmi",
		"sources.force": "package=rapl", "agent.sample_interval": "2s", "server.metrics_auth": "true", "indicators.wue_l_per_kwh": "",
	} {
		if v[k] != want {
			t.Errorf("%s = %q, want %q", k, v[k], want)
		}
	}
	// A file without the section gets it.
	out, err = Edit([]byte("agent:\n  sample_interval: 1s\n"), map[string]string{"indicators.tariff.currency": "USD"})
	if err != nil || !strings.Contains(string(out), "currency: USD") {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, err := Edit(nil, map[string]string{"logging.level": "debug"}); err != nil || !strings.Contains(string(out), "level: debug") {
		t.Fatalf("empty file: %v\n%s", err, out)
	}
	for _, bad := range []map[string]string{{"agent.nope": "1"}, {"server.enabled": "maybe"}, {"agent.sample_interval": "soon"}, {"backends": "x"}, {"sources.force": "rapl"}} {
		if _, err := Edit(data, bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	if _, err := Edit([]byte("- a\n- b\n"), map[string]string{"logging.level": "debug"}); err == nil {
		t.Error("accepted a list as configuration")
	}
}

func TestIndicatorsOverTime(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "profile.csv")
	var b strings.Builder
	b.WriteString("weekday,hour,g_co2e_per_kwh\n")
	for day := 1; day <= 7; day++ {
		for h := range 24 {
			fmt.Fprintf(&b, "%d,%d,%d\n", day, h, 400+h)
		}
	}
	if err := os.WriteFile(profile, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "greenlens.yaml")
	text := "indicators:\n  hourly_profile_file: " + profile + "\n  timezone: America/Mexico_City\n  tariff:\n    price_per_kwh: 1.5\n" +
		"    periods:\n      - days: [mon, tue, wed, thu, fri]\n        from: \"18:00\"\n        to: \"22:00\"\n        price_per_kwh: 3\n"
	if err := os.WriteFile(cfgPath, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(cfgPath, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	f := cfg.Factors()
	if f.Profile == nil || f.Profile[time.Monday][5] != 405 || f.Location.String() != "America/Mexico_City" || len(f.Periods) != 1 ||
		!f.Periods[0].Days[time.Friday] || f.Periods[0].Days[time.Saturday] || f.Periods[0].From != 18*60 || f.Periods[0].To != 22*60 {
		t.Fatalf("factors %+v", f)
	}
	// Without a time zone, the zone of the system, by its name.
	if err := os.WriteFile(cfgPath, []byte("indicators:\n  pue: 1.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tz := func(k string) string {
		if k == "TZ" {
			return ":Europe/Madrid"
		}
		return ""
	}
	if cfg, _, err := Load(cfgPath, tz); err != nil || cfg.Factors().Location.String() != "Europe/Madrid" || cfg.Factors().Varies() {
		t.Fatalf("defaults: %v %+v", err, cfg.Factors())
	}
	if cfg, _, err := Load(cfgPath, func(string) string { return "" }); err != nil || cfg.Factors().Location == nil {
		t.Fatalf("zone of the system: %v", err)
	}
	for _, tt := range []struct{ yaml, key, want string }{
		{"indicators:\n  timezone: Mars/Olympus\n", "indicators.timezone", "unknown time zone"},
		{"indicators:\n  hourly_profile_file: " + filepath.Join(dir, "none.csv") + "\n", "indicators.hourly_profile_file", "no such file"},
		{"indicators:\n  hourly_profile_file: profile.csv\n", "indicators.hourly_profile_file", "absolute path"},
		{"indicators:\n  tariff:\n    periods:\n      - days: [mon]\n        from: \"18:00\"\n        to: \"22:00\"\n        price_per_kwh: 3\n", "indicators.tariff.periods", "no period covers sun at 00:00"},
		{"indicators:\n  tariff:\n    price_per_kwh: 1\n    periods:\n      - days: [lunes]\n        from: \"6pm\"\n        to: \"22:00\"\n", "indicators.tariff.periods", "unknown day"},
		{"indicators:\n  tariff:\n    price_per_kwh: 1\n    periods:\n      - days: [mon]\n        from: \"18:00\"\n        to: \"20:00\"\n      - days: [mon, tue]\n        from: \"19:00\"\n        to: \"21:00\"\n", "indicators.tariff.periods", "overlap on mon at 19:00"},
		{"indicators:\n  sci:\n    functional_unit: watt\n", "indicators.sci.functional_unit", "must be one of"},
		{"indicators:\n  sci:\n    embodied:\n      server_kgco2e: 1200\n", "indicators.sci.embodied.lifetime_years", "is needed"},
		{"indicators:\n  sci:\n    embodied:\n      gpu_kgco2e: -1\n      lifetime_years: 4\n", "indicators.sci.embodied.gpu_kgco2e", "zero or more"},
		{"indicators:\n  sci:\n    embodied:\n      lifetime_years: 0\n", "indicators.sci.embodied.lifetime_years", "more than 0"},
	} {
		if err := os.WriteFile(cfgPath, []byte(tt.yaml), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := Load(cfgPath, func(string) string { return "" })
		if err == nil || !strings.Contains(err.Error(), tt.key) || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v", tt.key, err)
		}
	}
}

func TestEmbodiedCarbon(t *testing.T) {
	cfg := Defaults()
	if cfg.Indicators.SCI.FunctionalUnit != "1k_generated_tokens" || cfg.EmbodiedCarbon().Configured() {
		t.Fatalf("defaults %+v", cfg.Indicators.SCI)
	}
	server, life := 1200.0, 4.0
	cfg.Indicators.SCI.Embodied = Embodied{ServerKgCO2e: &server, LifetimeYears: &life}
	if err := cfg.Validate(); err != nil || !cfg.EmbodiedCarbon().Configured() || *cfg.EmbodiedCarbon().ServerKg != 1200 {
		t.Fatalf("%v %+v", err, cfg.EmbodiedCarbon())
	}
}
