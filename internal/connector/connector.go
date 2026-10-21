// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package connector reads the counters of inference backends: tokens and
// requests per model, the requests in progress and the models loaded. It
// only talks to the endpoints of the backends, with short timeouts and
// bounded responses, and never reads the content of requests.
package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/discovery"
	"github.com/MarioUrenaGarcia/greenlens/internal/promtext"
)

// Timeout bounds every request to a backend.
const Timeout = 2 * time.Second

// MaxBody bounds every response of a backend.
const MaxBody = 8 << 20

// Counters are the cumulative counters of one model.
type Counters struct {
	Model           string  `json:"model"`
	PromptTokens    float64 `json:"prompt_tokens"`
	GeneratedTokens float64 `json:"generated_tokens"`
	Requests        float64 `json:"requests"`
	// Consumer is set on counters of the proxy that identify consumers.
	Consumer string `json:"consumer,omitempty"`
}

// Snapshot is the state of a backend at one moment.
type Snapshot struct {
	Version string     `json:"version,omitempty"`
	Models  []Counters `json:"models,omitempty"`
	// Active is the number of requests in progress, when the engine
	// reports it.
	Active float64  `json:"active"`
	Loaded []string `json:"loaded,omitempty"`
	// Digests identify the files of the loaded models, when the engine
	// reports them, so a model replaced under the same name is noticed.
	Digests map[string]string `json:"digests,omitempty"`
	// TokenCounters is false for engines that do not count tokens, such
	// as Ollama, whose tokens come from the proxy.
	TokenCounters bool `json:"token_counters"`
	// FromZero is true for counters that started at zero when the agent
	// did, as those of the proxy, so their first reading is all new.
	FromZero bool `json:"-"`
}

// ErrNoMetrics means the backend answers but does not expose metrics, such
// as llama-server without --metrics or SGLang without --enable-metrics.
var ErrNoMetrics = errors.New("metrics are not enabled")

// Client returns the HTTP client used for backends: short timeout, no
// redirects to other hosts.
func Client() *http.Client {
	return &http.Client{
		Timeout: Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 0 && req.URL.Host != via[0].URL.Host {
				return errors.New("redirect to another host refused")
			}
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

// StatusError is a non-success answer of a backend.
type StatusError struct {
	URL    string
	Status int
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s: HTTP %d", e.URL, e.Status)
}

func get(ctx context.Context, c *http.Client, endpoint, path string) ([]byte, error) {
	u, err := url.JoinPath(endpoint, path)
	if err != nil {
		return nil, err
	}
	if q := strings.IndexByte(path, '?'); q >= 0 {
		u, err = url.JoinPath(endpoint, path[:q])
		if err != nil {
			return nil, err
		}
		u += path[q:]
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxBody {
		return nil, fmt.Errorf("%s: response larger than %d bytes", u, MaxBody)
	}
	if resp.StatusCode != http.StatusOK {
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, &StatusError{URL: u, Status: resp.StatusCode, Body: snippet}
	}
	return body, nil
}

func getJSON(ctx context.Context, c *http.Client, endpoint, path string, v any) error {
	body, err := get(ctx, c, endpoint, path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func metrics(ctx context.Context, c *http.Client, endpoint, path string) ([]promtext.Sample, error) {
	body, err := get(ctx, c, endpoint, path)
	var se *StatusError
	if errors.As(err, &se) && (se.Status == http.StatusNotFound || se.Status == http.StatusNotImplemented) {
		return nil, ErrNoMetrics
	}
	if err != nil {
		return nil, err
	}
	return promtext.Parse(strings.NewReader(string(body)))
}

// Scrape reads a backend of the given kind at endpoint.
func Scrape(ctx context.Context, c *http.Client, kind discovery.Kind, endpoint string) (Snapshot, error) {
	switch kind {
	case discovery.VLLM:
		return scrapeVLLM(ctx, c, endpoint)
	case discovery.LlamaCpp:
		return scrapeLlamaCpp(ctx, c, endpoint)
	case discovery.TGI:
		return scrapeTGI(ctx, c, endpoint)
	case discovery.SGLang:
		return scrapeSGLang(ctx, c, endpoint)
	case discovery.Ollama:
		return scrapeOllama(ctx, c, endpoint)
	case discovery.OpenAI, discovery.LocalAI:
		return scrapeOpenAI(ctx, c, endpoint)
	case discovery.Process:
		return Snapshot{}, nil
	}
	return Snapshot{}, fmt.Errorf("unknown backend kind %q", kind)
}

// perModel builds counters from metrics labeled with the model name.
func perModel(samples []promtext.Sample, label, prompt, generated, requests string) []Counters {
	p := promtext.Sum(samples, prompt, label)
	g := promtext.Sum(samples, generated, label)
	r := promtext.Sum(samples, requests, label)
	seen := map[string]bool{}
	var out []Counters
	for _, m := range []map[string]float64{p, g, r} {
		for model := range m {
			if seen[model] {
				continue
			}
			seen[model] = true
			out = append(out, Counters{Model: model, PromptTokens: p[model], GeneratedTokens: g[model], Requests: r[model]})
		}
	}
	return out
}

func gauge(samples []promtext.Sample, name string) float64 {
	total := 0.0
	for _, v := range promtext.Sum(samples, name, "") {
		total += v
	}
	return total
}

// vLLM: counters labeled model_name (vllm/v1/metrics/loggers.py), and
// GET /version.
func scrapeVLLM(ctx context.Context, c *http.Client, endpoint string) (Snapshot, error) {
	s, err := metrics(ctx, c, endpoint, "/metrics")
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{
		Models:        perModel(s, "model_name", "vllm:prompt_tokens_total", "vllm:generation_tokens_total", "vllm:request_success_total"),
		Active:        gauge(s, "vllm:num_requests_running"),
		TokenCounters: true,
	}
	var v struct {
		Version string `json:"version"`
	}
	if getJSON(ctx, c, endpoint, "/version", &v) == nil {
		snap.Version = v.Version
	}
	for _, m := range snap.Models {
		snap.Loaded = append(snap.Loaded, m.Model)
	}
	return snap, nil
}

// SGLang: counters labeled model_name, served with --enable-metrics.
func scrapeSGLang(ctx context.Context, c *http.Client, endpoint string) (Snapshot, error) {
	s, err := metrics(ctx, c, endpoint, "/metrics")
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{
		Models:        perModel(s, "model_name", "sglang:prompt_tokens_total", "sglang:generation_tokens_total", "sglang:num_requests_total"),
		Active:        gauge(s, "sglang:num_running_reqs"),
		TokenCounters: true,
	}
	for _, m := range snap.Models {
		snap.Loaded = append(snap.Loaded, m.Model)
	}
	return snap, nil
}

// llama.cpp: unlabeled counters (tools/server/README.md) served with
// --metrics. The model comes from /props; in router mode, /metrics needs
// ?model= for every model listed by /v1/models.
func scrapeLlamaCpp(ctx context.Context, c *http.Client, endpoint string) (Snapshot, error) {
	snap := Snapshot{TokenCounters: true}
	s, err := metrics(ctx, c, endpoint, "/metrics")
	var se *StatusError
	if errors.As(err, &se) && se.Status == http.StatusBadRequest {
		var list struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := getJSON(ctx, c, endpoint, "/v1/models", &list); err != nil {
			return Snapshot{}, err
		}
		for _, m := range list.Data {
			ms, err := metrics(ctx, c, endpoint, "/metrics?model="+url.QueryEscape(m.ID))
			if err != nil {
				return Snapshot{}, err
			}
			snap.Models = append(snap.Models, llamaCounters(ms, m.ID))
			snap.Active += gauge(ms, "llamacpp:requests_processing")
			snap.Loaded = append(snap.Loaded, m.ID)
		}
		return snap, nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	model := "default"
	var props struct {
		ModelPath string `json:"model_path"`
	}
	if getJSON(ctx, c, endpoint, "/props", &props) == nil && props.ModelPath != "" {
		model = props.ModelPath[strings.LastIndexAny(props.ModelPath, `/\`)+1:]
	}
	snap.Models = []Counters{llamaCounters(s, model)}
	snap.Active = gauge(s, "llamacpp:requests_processing")
	snap.Loaded = []string{model}
	return snap, nil
}

func llamaCounters(s []promtext.Sample, model string) Counters {
	return Counters{Model: model, PromptTokens: gauge(s, "llamacpp:prompt_tokens_total"), GeneratedTokens: gauge(s, "llamacpp:tokens_predicted_total")}
}

// TGI: histograms of input length and generated tokens, unlabeled; the
// model comes from /info. Recent versions serve metrics on a separate
// port, 9000 by default.
func scrapeTGI(ctx context.Context, c *http.Client, endpoint string) (Snapshot, error) {
	var info struct {
		ModelID string `json:"model_id"`
		Version string `json:"version"`
	}
	if err := getJSON(ctx, c, endpoint, "/info", &info); err != nil {
		return Snapshot{}, err
	}
	s, err := metrics(ctx, c, endpoint, "/metrics")
	if errors.Is(err, ErrNoMetrics) {
		if u, perr := url.Parse(endpoint); perr == nil {
			u.Host = u.Hostname() + ":9000"
			s, err = metrics(ctx, c, u.String(), "/metrics")
		}
	}
	if err != nil {
		return Snapshot{}, err
	}
	model := info.ModelID
	return Snapshot{
		Version: info.Version,
		Models: []Counters{{
			Model:           model,
			PromptTokens:    gauge(s, "tgi_request_input_length_sum"),
			GeneratedTokens: gauge(s, "tgi_request_generated_tokens_sum"),
			Requests:        gauge(s, "tgi_request_success"),
		}},
		Active:        gauge(s, "tgi_batch_current_size"),
		Loaded:        []string{model},
		TokenCounters: true,
	}, nil
}

// Ollama: GET /api/version and /api/ps (docs/api.md). It has no token
// counters; the proxy counts them from each response.
func scrapeOllama(ctx context.Context, c *http.Client, endpoint string) (Snapshot, error) {
	var v struct {
		Version string `json:"version"`
	}
	if err := getJSON(ctx, c, endpoint, "/api/version", &v); err != nil {
		return Snapshot{}, err
	}
	var ps struct {
		Models []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"models"`
	}
	if err := getJSON(ctx, c, endpoint, "/api/ps", &ps); err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{Version: v.Version}
	for _, m := range ps.Models {
		snap.Loaded = append(snap.Loaded, m.Name)
		if m.Digest != "" {
			if snap.Digests == nil {
				snap.Digests = map[string]string{}
			}
			snap.Digests[m.Name] = m.Digest
		}
	}
	return snap, nil
}

// OpenAI-compatible servers list their models on /v1/models; tokens come
// from the proxy.
func scrapeOpenAI(ctx context.Context, c *http.Client, endpoint string) (Snapshot, error) {
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := getJSON(ctx, c, endpoint, "/v1/models", &list); err != nil {
		return Snapshot{}, err
	}
	var snap Snapshot
	for _, m := range list.Data {
		snap.Loaded = append(snap.Loaded, m.ID)
	}
	return snap, nil
}
