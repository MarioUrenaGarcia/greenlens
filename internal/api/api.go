// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package api defines the documents the agent serves, shared by the agent
// and every client. Field names are stable; SchemaVersion changes only
// when a field changes meaning or disappears.
package api

import (
	"fmt"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/attribution"
	"github.com/MarioUrenaGarcia/greenlens/internal/bench"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/helpdocs"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
	"github.com/MarioUrenaGarcia/greenlens/internal/usage"
)

// SchemaVersion of every document.
const SchemaVersion = 1

// Paths of the agent API.
const (
	PathStatus       = "/api/v1/status"
	PathSources      = "/api/v1/sources"
	PathUsage        = "/api/v1/usage"
	PathCalibrations = "/api/v1/calibrations"
	PathLive         = "/api/v1/live"
	PathStream       = "/api/v1/stream"
	PathBackends     = "/api/v1/backends"
	PathBenchmarks   = "/api/v1/benchmarks"
	PathBenchRun     = "/api/v1/benchmarks/run"
	PathBenchResults = "/api/v1/benchmarks/results"
	PathBackup       = "/api/v1/backup"
	PathTokens       = "/api/v1/tokens" //nolint:gosec // a path, not a credential
	PathAudit        = "/api/v1/audit"
	PathSession      = "/api/v1/session"
	PathSessionCodes = "/api/v1/session/codes"
	PathLogin        = "/login"
	PathMetrics      = "/metrics"
	PathOpenAPI      = "/api/v1/openapi.yaml"
	PathCatalogs     = "/api/v1/catalogs"
	PathAbout        = "/api/v1/about"
	PathConfig       = "/api/v1/config"
	PathHelp         = "/api/v1/help"
	PathConfigCheck  = "/api/v1/config/preview"
	PathHealth       = "/healthz"
	PathReady        = "/readyz"
)

// HeaderSchemaVersion carries the schema version of the database in a
// backup response.
const HeaderSchemaVersion = "X-GreenLens-Schema-Version"

// HeaderCSRF carries the CSRF token of a browser session on requests that
// change something.
const HeaderCSRF = "X-GreenLens-CSRF" //nolint:gosec // the name of a header, not a credential

// SessionCookie holds the identifier of a browser session.
const SessionCookie = "greenlens_session" //nolint:gosec // the name of a cookie, not a credential

// Problem is a coded problem with its placeholder values.
type Problem struct {
	Code    help.Code         `json:"code"`
	Args    map[string]string `json:"args,omitempty"`
	Channel string            `json:"channel,omitempty"`
	Backend string            `json:"backend,omitempty"`
}

// FromProblem converts a source problem.
func FromProblem(p source.Problem) Problem {
	out := Problem{Code: p.Code, Channel: p.Channel}
	for i := 0; i+1 < len(p.Args); i += 2 {
		if k, ok := p.Args[i].(string); ok {
			if out.Args == nil {
				out.Args = map[string]string{}
			}
			out.Args[k] = fmt.Sprint(p.Args[i+1])
		}
	}
	return out
}

// Error returns the problem as a coded error, for rendering.
func (p Problem) Error() *help.Error {
	var args []any
	for k, v := range p.Args {
		args = append(args, k, v)
	}
	return help.New(p.Code, args...)
}

// Channel describes one probed channel and the decision about it.
type Channel struct {
	ID         string            `json:"id"`
	Source     string            `json:"source"`
	Domain     source.Domain     `json:"domain"`
	Instance   string            `json:"instance,omitempty"`
	Quality    source.Quality    `json:"quality"`
	Method     source.Method     `json:"method"`
	Label      string            `json:"label,omitempty"`
	Selected   bool              `json:"selected"`
	Reason     string            `json:"reason"`
	ReasonArgs map[string]string `json:"reason_args,omitempty"`
}

// Domain is the power of one domain in the latest sample and its energy
// since the agent started.
type Domain struct {
	Domain  source.Domain  `json:"domain"`
	Level   string         `json:"level"`
	PowerW  float64        `json:"power_w"`
	EnergyJ float64        `json:"energy_j"`
	Quality source.Quality `json:"quality"`
}

// Calibration summarizes the idle baseline.
type Calibration struct {
	At       time.Time            `json:"at"`
	Baseline attribution.Baseline `json:"baseline"`
	// IdleNowW is the current estimate of the idle power of the package,
	// the 5th percentile of the last 24 hours.
	IdleNowW float64 `json:"idle_now_w,omitempty"`
	Drift    float64 `json:"drift,omitempty"`
}

// Self is the consumption of the agent itself.
type Self struct {
	EnergyJ    float64 `json:"energy_j"`
	CPUSeconds float64 `json:"cpu_seconds"`
	RSSBytes   uint64  `json:"rss_bytes"`
}

// Factors are the conversion factors in force, so clients can turn energy
// into emissions, cost and water as the agent does.
type Factors struct {
	EmissionGPerKWh float64  `json:"emission_g_per_kwh"`
	EmissionSource  string   `json:"emission_source"`
	PUE             float64  `json:"pue"`
	TariffPerKWh    *float64 `json:"tariff_per_kwh,omitempty"`
	Currency        string   `json:"currency"`
	WUELPerKWh      *float64 `json:"wue_l_per_kwh,omitempty"`
}

// Catalog holds the messages of one language for the graphical interface.
type Catalog struct {
	SchemaVersion int               `json:"schema_version"`
	Lang          string            `json:"lang"`
	Languages     []string          `json:"languages"`
	Messages      map[string]string `json:"messages"`
}

// Status is the document of PathStatus.
type Status struct {
	SchemaVersion int          `json:"schema_version"`
	Version       string       `json:"version"`
	StartedAt     time.Time    `json:"started_at"`
	IntervalMS    int64        `json:"interval_ms"`
	SampleTime    time.Time    `json:"sample_time"`
	Samples       uint64       `json:"samples"`
	Estimated     bool         `json:"estimated"`
	Domains       []Domain     `json:"domains"`
	Channels      []Channel    `json:"channels"`
	Problems      []Problem    `json:"problems,omitempty"`
	Calibration   *Calibration `json:"calibration,omitempty"`
	Self          Self         `json:"self"`
	IdlePolicy    string       `json:"idle_policy"`
	Factors       Factors      `json:"factors"`
	// CPUJPerBusyS is the energy of one busy processor second over about
	// the last minute, without idle power when the node is calibrated.
	CPUJPerBusyS float64   `json:"cpu_joules_per_busy_second"`
	Backends     []Backend `json:"backends,omitempty"`
	Proxy        *Proxy    `json:"proxy,omitempty"`
}

// Usage is the document of PathUsage.
type Usage struct {
	SchemaVersion int                   `json:"schema_version"`
	From          time.Time             `json:"from"`
	To            time.Time             `json:"to"`
	BucketMS      int64                 `json:"bucket_ms"`
	Resolution    string                `json:"resolution"`
	Rows          []store.ChannelEnergy `json:"rows"`
}

// LivePoint is the power of every domain in one sample of the last
// minutes, kept in memory by the agent.
type LivePoint struct {
	Time       time.Time                 `json:"time"`
	IntervalMS int64                     `json:"interval_ms"`
	PowerW     map[source.Domain]float64 `json:"power_w"`
}

// Live is the document of PathLive.
type Live struct {
	SchemaVersion int         `json:"schema_version"`
	Points        []LivePoint `json:"points"`
}

// ErrorBody is returned with every error status.
type ErrorBody struct {
	Error string `json:"error"`
}

// Model is the use of one model of a backend since the agent started.
type Model struct {
	Name            string  `json:"name"`
	EnergyJ         float64 `json:"energy_j"`
	PromptTokens    int64   `json:"prompt_tokens"`
	GeneratedTokens int64   `json:"generated_tokens"`
	Requests        int64   `json:"requests"`
	// JoulesPerToken is the energy per generated token, zero when no
	// token was counted.
	JoulesPerToken float64 `json:"joules_per_generated_token"`
	Method         string  `json:"method"`
	Quality        string  `json:"quality"`
	// Regression is the energy per prompt and per generated token, when
	// the fit over the last day is good enough.
	Regression *usage.Regression `json:"regression,omitempty"`
}

// Backend is an inference server with its use since the agent started.
type Backend struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Endpoint    string `json:"endpoint,omitempty"`
	Static      bool   `json:"static"`
	ContainerID string `json:"container_id,omitempty"`
	Processes   int    `json:"processes"`
	// Roots are the root processes of the backend; their descendants
	// belong to it too.
	Roots         []Process `json:"roots,omitempty"`
	Version       string    `json:"version,omitempty"`
	TokenCounters bool      `json:"token_counters"`
	LastRead      time.Time `json:"last_read,omitzero"`
	Problem       *Problem  `json:"problem,omitempty"`
	// PowerW is the mean power of the backend in the last usage interval.
	PowerW  float64 `json:"power_w"`
	EnergyJ float64 `json:"energy_j"`
	CPUJ    float64 `json:"cpu_energy_j"`
	DRAMJ   float64 `json:"dram_energy_j"`
	GPUJ    float64 `json:"gpu_energy_j"`
	Models  []Model `json:"models,omitempty"`
}

// Process is a process of a backend.
type Process struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
}

// Proxy is the state of the token-counting proxy.
type Proxy struct {
	Listen         string `json:"listen"`
	Upstream       string `json:"upstream"`
	Backend        string `json:"backend"`
	Requests       uint64 `json:"requests"`
	EnergyHeader   bool   `json:"energy_header"`
	ConsumerHeader string `json:"consumer_header,omitempty"`
}

// Backends lists the backends of the node.
type Backends struct {
	SchemaVersion int       `json:"schema_version"`
	IntervalMS    int64     `json:"interval_ms"`
	Backends      []Backend `json:"backends"`
}

// BackendUsage is the usage of a period grouped by backend or model.
type BackendUsage struct {
	SchemaVersion int                 `json:"schema_version"`
	From          time.Time           `json:"from"`
	To            time.Time           `json:"to"`
	GroupBy       string              `json:"group_by"`
	BucketMS      int64               `json:"bucket_ms"`
	Resolution    string              `json:"resolution"`
	Rows          []store.UsageEnergy `json:"rows"`
}

// Benchmarks lists stored benchmarks, newest first.
type Benchmarks struct {
	SchemaVersion int            `json:"schema_version"`
	Benchmarks    []bench.Result `json:"benchmarks"`
}

// Token describes an access token without its secret.
type Token struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Role      string     `json:"role"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Tokens is the document of GET PathTokens.
type Tokens struct {
	SchemaVersion int     `json:"schema_version"`
	Tokens        []Token `json:"tokens"`
}

// CreateToken is the body of POST PathTokens.
type CreateToken struct {
	Name string `json:"name"`
	Role string `json:"role"`
	// ExpiresInS is the lifetime in seconds; zero never expires.
	ExpiresInS int64 `json:"expires_in_s,omitempty"`
}

// CreatedToken is the answer to POST PathTokens. The secret is shown only
// in this answer.
type CreatedToken struct {
	SchemaVersion int    `json:"schema_version"`
	Token         Token  `json:"token"`
	Secret        string `json:"secret"`
}

// Login is the body of POST PathSession.
type Login struct {
	Token string `json:"token"`
}

// Session describes who sends a request.
type Session struct {
	SchemaVersion int    `json:"schema_version"`
	Role          string `json:"role"`
	Actor         string `json:"actor"`
	Via           string `json:"via"`
	// CSRF must be sent in HeaderCSRF by a browser session on requests
	// that change something.
	CSRF string `json:"csrf,omitempty"`
}

// SessionCodeRequest is the body of POST PathSessionCodes.
type SessionCodeRequest struct {
	Role string `json:"role"`
}

// SessionCode is a one-time sign-in link for the graphical interface.
type SessionCode struct {
	SchemaVersion int       `json:"schema_version"`
	URL           string    `json:"url"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// Audit is the document of PathAudit, newest entries first.
type Audit struct {
	SchemaVersion int                `json:"schema_version"`
	Entries       []store.AuditEntry `json:"entries"`
}

// BenchmarkRequest asks the agent to run a benchmark.
type BenchmarkRequest struct {
	Kind      string  `json:"kind"`
	Endpoint  string  `json:"endpoint,omitempty"`
	Model     string  `json:"model"`
	Runs      int     `json:"runs"`
	MaxTokens int     `json:"max_tokens"`
	WarmupS   float64 `json:"warmup_s"`
	MinRunS   float64 `json:"min_run_s"`
}

// BenchmarkJob is the state of the benchmark run by the agent.
type BenchmarkJob struct {
	SchemaVersion int               `json:"schema_version"`
	State         string            `json:"state"`
	Run           int               `json:"run"`
	Runs          int               `json:"runs"`
	Request       *BenchmarkRequest `json:"request,omitempty"`
	StartedAt     *time.Time        `json:"started_at,omitempty"`
	EndedAt       *time.Time        `json:"ended_at,omitempty"`
	BenchmarkID   int64             `json:"benchmark_id,omitempty"`
	Error         string            `json:"error,omitempty"`
}

// ConfigField is one key of the configuration with its value in force.
type ConfigField struct {
	Key     string   `json:"key"`
	Type    string   `json:"type"`
	Value   string   `json:"value"`
	Default string   `json:"default"`
	Env     string   `json:"env,omitempty"`
	Enum    []string `json:"enum,omitempty"`
}

// ConfigDoc is the configuration of the agent. Values are written as for
// their environment variable.
type ConfigDoc struct {
	SchemaVersion int    `json:"schema_version"`
	Path          string `json:"path"`
	Writable      bool   `json:"writable"`
	// Reason says why the file cannot be written: no_file or read_only.
	Reason  string        `json:"reason,omitempty"`
	YAML    string        `json:"yaml"`
	Fields  []ConfigField `json:"fields"`
	Restart []string      `json:"restart_keys"`
}

// ConfigChange sets keys of the configuration.
type ConfigChange struct {
	Changes map[string]string `json:"changes"`
}

// ConfigError is a problem of a configuration.
type ConfigError struct {
	Key     string `json:"key,omitempty"`
	Message string `json:"message"`
}

// ConfigPreview is the configuration file that changes would produce.
type ConfigPreview struct {
	SchemaVersion int           `json:"schema_version"`
	YAML          string        `json:"yaml"`
	Valid         bool          `json:"valid"`
	Errors        []ConfigError `json:"errors"`
	NeedsRestart  bool          `json:"needs_restart"`
	Applied       bool          `json:"applied"`
}

// HelpIndex lists the pages of the embedded documentation.
type HelpIndex struct {
	SchemaVersion int             `json:"schema_version"`
	Lang          string          `json:"lang"`
	Sections      []string        `json:"sections"`
	Pages         []helpdocs.Page `json:"pages"`
}

// HelpSearch is the answer to a search of the documentation.
type HelpSearch struct {
	SchemaVersion int               `json:"schema_version"`
	Lang          string            `json:"lang"`
	Query         string            `json:"query"`
	Results       []helpdocs.Result `json:"results"`
}

// HelpPage is one page rendered to HTML. Links between pages are routes of
// the interface, as #/help/guides/agent.
type HelpPage struct {
	SchemaVersion int    `json:"schema_version"`
	Lang          string `json:"lang"`
	Path          string `json:"path"`
	Title         string `json:"title"`
	HTML          string `json:"html"`
}
