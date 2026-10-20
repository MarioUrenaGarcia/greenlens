// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package config loads the GreenLens configuration. Values come, from the
// highest precedence to the lowest, from command line flags, GREENLENS_*
// environment variables, the configuration file and the defaults. Unknown
// keys in the file are errors, so a typo never goes unnoticed.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/MarioUrenaGarcia/greenlens/internal/attribution"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/selector"
)

// Duration is a time.Duration written as "1s", "10m" or "168h".
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	*d = Duration(v)
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// Config is the whole configuration file.
type Config struct {
	Agent       Agent       `yaml:"agent"`
	Sources     Sources     `yaml:"sources"`
	Attribution Attribution `yaml:"attribution"`
	Indicators  Indicators  `yaml:"indicators"`
	Retention   Retention   `yaml:"retention"`
	Logging     Logging     `yaml:"logging"`
}

// Agent configures the measuring agent.
type Agent struct {
	SampleInterval Duration `yaml:"sample_interval"`
	DataDir        string   `yaml:"data_dir"`
	Socket         string   `yaml:"socket"`
	HostRoot       string   `yaml:"host_root"`
}

// Sources configures the energy sources.
type Sources struct {
	Auto     bool              `yaml:"auto"`
	Disable  []string          `yaml:"disable"`
	Force    map[string]string `yaml:"force"`
	RAPL     RAPL              `yaml:"rapl"`
	Estimate Estimate          `yaml:"estimate"`
}

// RAPL configures the RAPL counters.
type RAPL struct {
	MinInterval Duration `yaml:"min_interval"`
}

// Estimate configures the CPU power model.
type Estimate struct {
	IdleWatts float64 `yaml:"idle_watts"`
	FullWatts float64 `yaml:"full_watts"`
	Exponent  float64 `yaml:"exponent"`
}

// Attribution configures how energy is split between processes.
type Attribution struct {
	IdlePolicy string `yaml:"idle_policy"`
}

// Indicators configures the conversion of energy into indicators.
type Indicators struct {
	EmissionFactorGPerKWh *float64 `yaml:"emission_factor_g_per_kwh"`
	EmissionFactorSource  string   `yaml:"emission_factor_source"`
	PUE                   float64  `yaml:"pue"`
	WUELPerKWh            *float64 `yaml:"wue_l_per_kwh"`
	Tariff                Tariff   `yaml:"tariff"`
}

// Tariff is the price of electricity.
type Tariff struct {
	PricePerKWh *float64 `yaml:"price_per_kwh"`
	Currency    string   `yaml:"currency"`
}

// Retention sets how long data is kept at each resolution.
type Retention struct {
	Raw      Duration `yaml:"raw"`
	Rollup1m Duration `yaml:"rollup_1m"`
	Rollup1h Duration `yaml:"rollup_1h"`
}

// Logging configures the agent logs.
type Logging struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// Defaults returns the configuration used when nothing is set.
func Defaults() Config {
	return Config{
		Agent:       Agent{SampleInterval: Duration(time.Second), HostRoot: "/"},
		Sources:     Sources{Auto: true, RAPL: RAPL{MinInterval: Duration(50 * time.Millisecond)}, Estimate: Estimate{Exponent: 1}},
		Attribution: Attribution{IdlePolicy: string(attribution.Dynamic)},
		Indicators:  Indicators{PUE: 1, Tariff: Tariff{Currency: "MXN"}},
		Retention: Retention{
			Raw:      Duration(168 * time.Hour),
			Rollup1m: Duration(2160 * time.Hour),
			Rollup1h: Duration(17520 * time.Hour),
		},
		Logging: Logging{Level: "info", Format: "json"},
	}
}

// Sampling limits of the agent.
const (
	MinSampleInterval = 50 * time.Millisecond
	MaxSampleInterval = 60 * time.Second
)

// Paths returns the configuration files searched when none is given, in
// order: the system file and the file of the user.
func Paths(getenv func(string) string) []string {
	out := []string{"/etc/greenlens/greenlens.yaml"}
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		out = append(out, filepath.Join(x, "greenlens", "greenlens.yaml"))
	} else if h := getenv("HOME"); h != "" {
		out = append(out, filepath.Join(h, ".config", "greenlens", "greenlens.yaml"))
	}
	return out
}

// Load reads the configuration. With an explicit path the file must exist;
// otherwise the first existing file of Paths is used, and the defaults
// apply when there is none. It returns the path that was read, if any.
func Load(path string, getenv func(string) string) (Config, string, error) {
	cfg := Defaults()
	if path == "" {
		for _, p := range Paths(getenv) {
			if _, err := os.Stat(p); err == nil {
				path = p
				break
			}
		}
	} else if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return cfg, path, help.New(help.GL3001, "path", path).Wrap(err)
	}
	if path != "" {
		data, err := os.ReadFile(path) //nolint:gosec // the path is chosen by the operator
		if err != nil {
			return cfg, path, help.New(help.GL3002, "path", path, "detail", err.Error()).Wrap(err)
		}
		if err := Parse(data, &cfg); err != nil {
			return cfg, path, help.New(help.GL3002, "path", path, "detail", err.Error()).Wrap(err)
		}
	}
	if err := ApplyEnv(&cfg, getenv); err != nil {
		return cfg, path, err
	}
	return cfg, path, cfg.Validate()
}

// Parse decodes YAML into cfg, keeping the values of cfg for keys the
// document does not set.
func Parse(data []byte, cfg *Config) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return err
	}
	return nil
}

// Factors returns the conversion factors, with the FESEN default when no
// emission factor is configured.
func (c Config) Factors() indicators.Factors {
	f := indicators.Default()
	if c.Indicators.EmissionFactorGPerKWh != nil {
		f.EmissionGPerKWh = *c.Indicators.EmissionFactorGPerKWh
		f.EmissionSource = c.Indicators.EmissionFactorSource
	}
	f.PUE = c.Indicators.PUE
	f.WUELPerKWh = c.Indicators.WUELPerKWh
	f.TariffPerKWh = c.Indicators.Tariff.PricePerKWh
	f.Currency = c.Indicators.Tariff.Currency
	return f
}

// SelectorOptions turns sources.disable, sources.force and sources.auto
// into options of the selector. With auto off, only forced sources are used.
func (c Config) SelectorOptions() selector.Options {
	opt := selector.Options{Disable: slices.Clone(c.Sources.Disable), Force: map[source.Domain]string{}}
	for d, id := range c.Sources.Force {
		opt.Force[source.Domain(d)] = id
	}
	if !c.Sources.Auto {
		for _, id := range SourceIDs {
			forced := false
			for _, f := range c.Sources.Force {
				forced = forced || f == id
			}
			if !forced && !slices.Contains(opt.Disable, id) {
				opt.Disable = append(opt.Disable, id)
			}
		}
	}
	return opt
}

// SourceIDs lists the source names accepted in sources.disable and
// sources.force.
var SourceIDs = []string{"rapl", "perf_event", "hwmon", "acpi_power_meter", "ipmi", "battery", "estimate"}

// ForcibleDomains lists the domains accepted as keys of sources.force.
var ForcibleDomains = []source.Domain{source.DomainPackage, source.DomainDRAM, source.DomainPSys, source.DomainPlatform}

// Validate checks every value and reports all problems at once.
func (c Config) Validate() error {
	var errs []error
	bad := func(key, detail string) {
		errs = append(errs, help.New(help.GL3003, "key", key, "detail", detail))
	}
	if d := c.Agent.SampleInterval.D(); d < MinSampleInterval || d > MaxSampleInterval {
		bad("agent.sample_interval", fmt.Sprintf("%s is outside %s to %s", d, MinSampleInterval, MaxSampleInterval))
	}
	if c.Agent.SampleInterval.D() < c.Sources.RAPL.MinInterval.D() {
		bad("agent.sample_interval", fmt.Sprintf("%s is below sources.rapl.min_interval (%s)", c.Agent.SampleInterval.D(), c.Sources.RAPL.MinInterval.D()))
	}
	if c.Agent.HostRoot == "" || !filepath.IsAbs(c.Agent.HostRoot) {
		bad("agent.host_root", "must be an absolute path")
	}
	for _, p := range []struct{ key, v string }{{"agent.data_dir", c.Agent.DataDir}, {"agent.socket", c.Agent.Socket}} {
		if p.v != "" && !filepath.IsAbs(p.v) {
			bad(p.key, "must be an absolute path")
		}
	}
	if c.Sources.RAPL.MinInterval.D() < MinSampleInterval {
		bad("sources.rapl.min_interval", fmt.Sprintf("must be at least %s", MinSampleInterval))
	}
	for _, id := range c.Sources.Disable {
		if !slices.Contains(SourceIDs, id) {
			bad("sources.disable", fmt.Sprintf("unknown source %q; valid: %s", id, strings.Join(SourceIDs, ", ")))
		}
	}
	for d, id := range c.Sources.Force {
		if !slices.Contains(ForcibleDomains, source.Domain(d)) {
			bad("sources.force", fmt.Sprintf("unknown domain %q", d))
		}
		if !slices.Contains(SourceIDs, id) {
			bad("sources.force", fmt.Sprintf("unknown source %q", id))
		}
	}
	e := c.Sources.Estimate
	if e.IdleWatts < 0 || e.FullWatts < 0 || (e.FullWatts > 0 && e.FullWatts < e.IdleWatts) {
		bad("sources.estimate", "idle_watts and full_watts must be positive, with full_watts at least idle_watts")
	}
	if (e.IdleWatts > 0) != (e.FullWatts > 0) {
		bad("sources.estimate", "set both idle_watts and full_watts, or neither")
	}
	if e.Exponent <= 0 || e.Exponent > 4 {
		bad("sources.estimate.exponent", "must be greater than 0 and at most 4")
	}
	if _, err := attribution.ParsePolicy(c.Attribution.IdlePolicy); err != nil {
		bad("attribution.idle_policy", "must be one of dynamic, proportional, full or wall")
	}
	if c.Indicators.EmissionFactorGPerKWh != nil && c.Indicators.EmissionFactorSource == "" {
		bad("indicators.emission_factor_source", "a configured emission factor needs its source, such as the publication and year")
	}
	if err := c.Factors().Validate(); err != nil {
		bad("indicators", err.Error())
	}
	if c.Indicators.Tariff.Currency == "" || len(c.Indicators.Tariff.Currency) != 3 || strings.ToUpper(c.Indicators.Tariff.Currency) != c.Indicators.Tariff.Currency {
		bad("indicators.tariff.currency", "must be an ISO 4217 code such as MXN or USD")
	}
	r := c.Retention
	if r.Raw.D() < time.Hour || r.Rollup1m.D() < r.Raw.D() || r.Rollup1h.D() < r.Rollup1m.D() {
		bad("retention", "raw must be at least 1h, and each resolution must be kept at least as long as the finer one")
	}
	if !slices.Contains([]string{"debug", "info", "warn", "error"}, c.Logging.Level) {
		bad("logging.level", "must be debug, info, warn or error")
	}
	if !c.Sources.Auto && len(c.Sources.Force) == 0 {
		bad("sources.auto", "with auto disabled, sources.force must name at least one source")
	}
	if !slices.Contains([]string{"json", "text"}, c.Logging.Format) {
		bad("logging.format", "must be json or text")
	}
	return errors.Join(errs...)
}
