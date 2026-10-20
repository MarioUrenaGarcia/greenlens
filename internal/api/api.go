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
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
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
	PathBackends     = "/api/v1/backends"
	PathHealth       = "/healthz"
	PathReady        = "/readyz"
)

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
	Backends      []Backend    `json:"backends,omitempty"`
	Proxy         *Proxy       `json:"proxy,omitempty"`
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
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	Endpoint      string    `json:"endpoint,omitempty"`
	Static        bool      `json:"static"`
	ContainerID   string    `json:"container_id,omitempty"`
	Processes     int       `json:"processes"`
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
	Resolution    string              `json:"resolution"`
	Rows          []store.UsageEnergy `json:"rows"`
}
