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

	"net"
	"net/url"
	"regexp"

	"go.yaml.in/yaml/v3"

	"github.com/MarioUrenaGarcia/greenlens/internal/attribution"
	"github.com/MarioUrenaGarcia/greenlens/internal/discovery"
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
	Backends    []Backend   `yaml:"backends"`
	Discovery   Discovery   `yaml:"discovery"`
	Proxy       Proxy       `yaml:"proxy"`
	Server      Server      `yaml:"server"`
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
	NVML     NVML              `yaml:"nvml"`
	Estimate Estimate          `yaml:"estimate"`
}

// RAPL configures the RAPL counters.
type RAPL struct {
	MinInterval Duration `yaml:"min_interval"`
}

// NVML configures the reading of NVIDIA GPUs.
type NVML struct {
	// Helper is the path of greenlens-nvml; empty looks next to the
	// greenlens binary and in the system directories.
	Helper string `yaml:"helper"`
}

// Estimate configures the CPU power model.
type Estimate struct {
	IdleWatts float64 `yaml:"idle_watts"`
	FullWatts float64 `yaml:"full_watts"`
	Exponent  float64 `yaml:"exponent"`
}

// Attribution configures how energy is split between processes.
type Attribution struct {
	IdlePolicy          string  `yaml:"idle_policy"`
	MaxModelsPerBackend int     `yaml:"max_models_per_backend"`
	MaxConsumers        int     `yaml:"max_consumers"`
	RegressionMinR2     float64 `yaml:"regression_min_r2"`
}

// Proxy configures the token-counting proxy run by the agent.
type Proxy struct {
	Enabled  bool   `yaml:"enabled"`
	Listen   string `yaml:"listen"`
	Upstream string `yaml:"upstream"`
	// PerRequestEnergyHeader adds X-GreenLens-Energy-Joules to responses.
	PerRequestEnergyHeader bool `yaml:"per_request_energy_header"`
	// ConsumerHeader names the request header that identifies a consumer,
	// such as Authorization; its value is kept only as an HMAC.
	ConsumerHeader      string `yaml:"consumer_header"`
	ConsumerAliasesFile string `yaml:"consumer_aliases_file"`
}

// Server configures the HTTP API and the graphical interface.
type Server struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
	// TokensFile holds the access tokens; empty keeps them in the data
	// directory, where the agent can add and revoke them.
	TokensFile string `yaml:"tokens_file"`
	// MetricsAuth asks for a token on /metrics too.
	MetricsAuth bool `yaml:"metrics_auth"`
	TLS         TLS  `yaml:"tls"`
}

// TLS names the certificate of the HTTP API. Without one the API speaks
// plain HTTP, which is meant for loopback and SSH tunnels.
type TLS struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// DefaultListen is the address of the HTTP API. Port 10060 follows the
// default port allocations of the Prometheus ecosystem.
const DefaultListen = "127.0.0.1:10060"

// IsLoopback reports whether a listen address accepts only connections
// from this machine. An empty host listens on every interface.
func IsLoopback(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil || host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Backend is an inference server declared in the configuration. Its name
// identifies it across restarts.
type Backend struct {
	Name     string `yaml:"name"`
	Kind     string `yaml:"kind"`
	Endpoint string `yaml:"endpoint"`
	// Process is the executable of a generic process backend, or of an
	// engine whose port is not visible from the agent, as in a container
	// with its own network.
	Process string `yaml:"process"`
}

// Discovery configures the search for backends.
type Discovery struct {
	Enabled  bool     `yaml:"enabled"`
	Interval Duration `yaml:"interval"`
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
		Attribution: Attribution{IdlePolicy: string(attribution.Dynamic), MaxModelsPerBackend: 50, MaxConsumers: 200, RegressionMinR2: 0.7},
		Proxy:       Proxy{Listen: "127.0.0.1:11435", Upstream: "http://127.0.0.1:11434"},
		Server:      Server{Enabled: true, Listen: DefaultListen},
		Discovery:   Discovery{Enabled: true, Interval: Duration(10 * time.Second)},
		Indicators:  Indicators{PUE: 1, Tariff: Tariff{Currency: "MXN"}},
		Retention: Retention{
			Raw:      Duration(168 * time.Hour),
			Rollup1m: Duration(2160 * time.Hour),
			Rollup1h: Duration(17520 * time.Hour),
		},
		Logging: Logging{Level: "info", Format: "json"},
	}
}

// Limits of the backend settings.
const (
	MaxModels            = 10000
	MinDiscoveryInterval = time.Second
	MaxDiscoveryInterval = 10 * time.Minute
)

var backendName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// headerName follows the token rule of RFC 9110.
var headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]{1,128}$")

func kindList() string {
	var out []string
	for _, k := range discovery.Kinds {
		out = append(out, string(k))
	}
	return strings.Join(out, ", ")
}

// CheckEndpoint accepts the base URL of a backend: http or https, a host,
// and nothing that could carry a secret or change the paths GreenLens asks
// for.
func CheckEndpoint(s string) error {
	u, err := url.Parse(s)
	switch {
	case err != nil:
		return fmt.Errorf("not a URL: %w", err)
	case u.Scheme != "http" && u.Scheme != "https":
		return errors.New("must start with http:// or https://")
	case u.Host == "" || u.Hostname() == "":
		return errors.New("has no host")
	case u.User != nil:
		return errors.New("must not contain credentials; backends are read without authentication")
	case u.RawQuery != "" || u.Fragment != "":
		return errors.New("must not have a query or a fragment")
	case u.Path != "" && u.Path != "/":
		return errors.New("must be the base URL of the server, without a path")
	}
	return nil
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
var SourceIDs = []string{"rapl", "perf_event", "hwmon", "hwmon_gpu", "nvml", "nvidia_smi", "acpi_power_meter", "ipmi", "battery", "estimate"}

// ForcibleDomains lists the domains accepted as keys of sources.force.
var ForcibleDomains = []source.Domain{source.DomainPackage, source.DomainDRAM, source.DomainGPU, source.DomainPSys, source.DomainPlatform}

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
	if h := c.Sources.NVML.Helper; h != "" && !filepath.IsAbs(h) {
		bad("sources.nvml.helper", "must be an absolute path")
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
	if m := c.Attribution.MaxModelsPerBackend; m < 1 || m > MaxModels {
		bad("attribution.max_models_per_backend", fmt.Sprintf("must be between 1 and %d", MaxModels))
	}
	if m := c.Attribution.MaxConsumers; m < 1 || m > MaxModels {
		bad("attribution.max_consumers", fmt.Sprintf("must be between 1 and %d", MaxModels))
	}
	if p := c.Proxy; p.Enabled {
		if _, port, err := net.SplitHostPort(p.Listen); err != nil || port == "" || port == "0" {
			bad("proxy.listen", "must be an address and port, such as 127.0.0.1:11435")
		}
		if err := CheckEndpoint(p.Upstream); err != nil {
			bad("proxy.upstream", err.Error())
		}
	}
	if sv := c.Server; sv.Enabled {
		if _, port, err := net.SplitHostPort(sv.Listen); err != nil || port == "" || port == "0" {
			bad("server.listen", "must be an address and port, such as "+DefaultListen)
		}
	}
	if f := c.Server.TokensFile; f != "" && !filepath.IsAbs(f) {
		bad("server.tokens_file", "must be an absolute path")
	}
	if t := c.Server.TLS; (t.CertFile == "") != (t.KeyFile == "") {
		bad("server.tls", "set both cert_file and key_file, or neither")
	} else {
		for _, p := range []struct{ key, v string }{{"server.tls.cert_file", t.CertFile}, {"server.tls.key_file", t.KeyFile}} {
			if p.v != "" && !filepath.IsAbs(p.v) {
				bad(p.key, "must be an absolute path")
			}
		}
	}
	if h := c.Proxy.ConsumerHeader; h != "" && !headerName.MatchString(h) {
		bad("proxy.consumer_header", "must be the name of an HTTP header")
	}
	if f := c.Proxy.ConsumerAliasesFile; f != "" && !filepath.IsAbs(f) {
		bad("proxy.consumer_aliases_file", "must be an absolute path")
	}
	if r := c.Attribution.RegressionMinR2; r < 0 || r > 1 {
		bad("attribution.regression_min_r2", "must be between 0 and 1")
	}
	if d := c.Discovery.Interval.D(); d < MinDiscoveryInterval || d > MaxDiscoveryInterval {
		bad("discovery.interval", fmt.Sprintf("%s is outside %s to %s", d, MinDiscoveryInterval, MaxDiscoveryInterval))
	}
	names := map[string]bool{}
	for i, b := range c.Backends {
		key := fmt.Sprintf("backends[%d]", i)
		if !backendName.MatchString(b.Name) {
			bad(key+".name", "must be 1 to 63 lowercase letters, digits, '.', '_' or '-', starting with a letter or digit")
		} else if names[b.Name] {
			bad(key+".name", fmt.Sprintf("%q is declared twice", b.Name))
		}
		names[b.Name] = true
		if !slices.Contains(discovery.Kinds, discovery.Kind(b.Kind)) {
			bad(key+".kind", fmt.Sprintf("unknown kind %q; valid: %s", b.Kind, kindList()))
		}
		if b.Endpoint != "" {
			if err := CheckEndpoint(b.Endpoint); err != nil {
				bad(key+".endpoint", err.Error())
			}
		}
		switch {
		case discovery.Kind(b.Kind) == discovery.Process && b.Process == "":
			bad(key+".process", "a process backend needs the name of its executable")
		case discovery.Kind(b.Kind) != discovery.Process && b.Endpoint == "" && b.Process == "":
			bad(key+".endpoint", "set the endpoint of the backend, or the name of its executable in process")
		}
		if b.Process != "" && (strings.ContainsAny(b.Process, "/\x00") || len(b.Process) > 255) {
			bad(key+".process", "must be the file name of the executable, without directories")
		}
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
