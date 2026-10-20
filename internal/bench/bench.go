// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package bench runs reproducible benchmarks of a model: the same prompts,
// with greedy decoding and a fixed seed, several times, measuring the
// energy of each run. Only the SHA-256 of the prompt set is kept, never the
// prompts.
package bench

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/discovery"
	"github.com/MarioUrenaGarcia/greenlens/internal/stats"
)

//go:embed prompts.txt
var defaultPrompts string

// DefaultPrompts returns the embedded prompt set: short, everyday tasks
// that every chat model can answer.
func DefaultPrompts() []string {
	p, _ := ParsePrompts([]byte(defaultPrompts))
	return p
}

// MaxPrompts bounds a prompt set.
const MaxPrompts = 10000

// ParsePrompts reads one prompt per line; blank lines are skipped.
func ParsePrompts(data []byte) ([]string, error) {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			out = append(out, l)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	switch {
	case len(out) == 0:
		return nil, errors.New("the prompt set is empty")
	case len(out) > MaxPrompts:
		return nil, fmt.Errorf("the prompt set has more than %d prompts", MaxPrompts)
	}
	return out, nil
}

// Hash identifies a prompt set: the SHA-256 of its prompts joined by
// newlines.
func Hash(prompts []string) string {
	sum := sha256.Sum256([]byte(strings.Join(prompts, "\n")))
	return hex.EncodeToString(sum[:])
}

// Target is the model benchmarked.
type Target struct {
	Kind     discovery.Kind `json:"kind"`
	Endpoint string         `json:"endpoint"`
	Model    string         `json:"model"`
}

// Params fix the generation, so runs can be repeated.
type Params struct {
	MaxTokens   int     `json:"max_tokens"`
	Temperature float64 `json:"temperature"`
	Seed        int     `json:"seed"`
	Runs        int     `json:"runs"`
	Warmup      int     `json:"warmup"`
}

// DefaultParams are greedy decoding with a fixed seed.
func DefaultParams() Params {
	return Params{MaxTokens: 128, Temperature: 0, Seed: 42, Runs: 5, Warmup: 1}
}

// RequestError is a backend that refused a prompt.
type RequestError struct {
	Status int
	Body   string
}

func (e *RequestError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body) }

// Tokens are the counts of one generation.
type Tokens struct {
	Prompt    int64
	Generated int64
}

// Generate sends one prompt and returns its tokens. Ollama is asked
// through its native API, which counts prompt tokens; the other engines
// through the OpenAI-compatible chat API they all serve.
func Generate(ctx context.Context, c *http.Client, t Target, p Params, prompt string) (Tokens, error) {
	var path string
	var body any
	switch t.Kind {
	case discovery.Ollama:
		path = "/api/generate"
		body = map[string]any{
			"model": t.Model, "prompt": prompt, "stream": false,
			"options": map[string]any{"temperature": p.Temperature, "seed": p.Seed, "num_predict": p.MaxTokens},
		}
	default:
		path = "/v1/chat/completions"
		body = map[string]any{
			"model": t.Model, "messages": []map[string]string{{"role": "user", "content": prompt}},
			"temperature": p.Temperature, "seed": p.Seed, "max_tokens": p.MaxTokens, "stream": false,
		}
	}
	u, err := url.JoinPath(t.Endpoint, path)
	if err != nil {
		return Tokens{}, err
	}
	b, err := json.Marshal(body)
	if err != nil {
		return Tokens{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return Tokens{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return Tokens{}, err
	}
	if resp.StatusCode != http.StatusOK {
		msg := string(data)
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return Tokens{}, &RequestError{Status: resp.StatusCode, Body: msg}
	}
	var r struct {
		PromptEvalCount int64 `json:"prompt_eval_count"`
		EvalCount       int64 `json:"eval_count"`
		Usage           struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return Tokens{}, fmt.Errorf("%s: %w", path, err)
	}
	if t.Kind == discovery.Ollama {
		return Tokens{Prompt: r.PromptEvalCount, Generated: r.EvalCount}, nil
	}
	return Tokens{Prompt: r.Usage.PromptTokens, Generated: r.Usage.CompletionTokens}, nil
}

// Energy is what a meter measured over one run.
type Energy struct {
	// TotalJ is the energy of the components of the node.
	TotalJ float64
	// IdleJ is the idle energy over the run, from calibration; zero
	// without one.
	IdleJ float64
}

// Meter measures the energy of the node while fn runs.
type Meter func(fn func() error) (Energy, time.Duration, error)

// Run is one pass over the prompt set.
type Run struct {
	EnergyJ         float64 `json:"energy_j"`
	DynamicJ        float64 `json:"dynamic_j"`
	DurationS       float64 `json:"duration_s"`
	PromptTokens    int64   `json:"prompt_tokens"`
	GeneratedTokens int64   `json:"generated_tokens"`
	JPerToken       float64 `json:"j_per_generated_token"`
}

// Bases of the energy per token.
const (
	BasisDynamic = "dynamic"
	BasisTotal   = "total"
)

// Result is a finished benchmark.
type Result struct {
	ID              int64         `json:"id,omitempty"`
	StartedAt       time.Time     `json:"started_at"`
	EndedAt         time.Time     `json:"ended_at"`
	Target          Target        `json:"target"`
	Params          Params        `json:"params"`
	PromptSetSHA256 string        `json:"prompt_set_sha256"`
	Prompts         int           `json:"prompts"`
	Context         Context       `json:"context"`
	Runs            []Run         `json:"runs"`
	Basis           string        `json:"basis"`
	Energy          stats.Summary `json:"energy_j"`
	JPerToken       stats.Summary `json:"j_per_generated_token"`
	GeneratedTokens int64         `json:"generated_tokens"`
	// Causes lists why the runs may differ when the coefficient of
	// variation exceeds UnstableCV.
	Causes []string `json:"causes,omitempty"`
}

// UnstableCV is the coefficient of variation above which the runs are not
// considered stable.
const UnstableCV = 0.10

// Bench runs warmup passes, which are not measured, then the measured
// runs, and summarizes them. calibrated says whether the meter subtracts a
// measured idle baseline; the energy per token is then on dynamic energy.
func Bench(ctx context.Context, c *http.Client, t Target, p Params, prompts []string, m Meter, calibrated bool, progress func(run int)) (Result, error) {
	if p.Runs < 2 {
		return Result{}, errors.New("a benchmark needs at least two runs")
	}
	res := Result{Target: t, Params: p, PromptSetSHA256: Hash(prompts), Prompts: len(prompts), StartedAt: time.Now(), Basis: BasisTotal}
	if calibrated {
		res.Basis = BasisDynamic
	}
	pass := func(r *Run) error {
		for _, pr := range prompts {
			tok, err := Generate(ctx, c, t, p, pr)
			if err != nil {
				return err
			}
			if r != nil {
				r.PromptTokens += tok.Prompt
				r.GeneratedTokens += tok.Generated
			}
		}
		return nil
	}
	for range p.Warmup {
		if err := pass(nil); err != nil {
			return res, err
		}
	}
	var energy, jpt []float64
	for i := range p.Runs {
		var r Run
		e, d, err := m(func() error { return pass(&r) })
		if err != nil {
			return res, err
		}
		r.EnergyJ, r.DurationS = e.TotalJ, d.Seconds()
		r.DynamicJ = max(0, e.TotalJ-e.IdleJ)
		basis := r.EnergyJ
		if calibrated {
			basis = r.DynamicJ
		}
		if r.GeneratedTokens > 0 {
			r.JPerToken = basis / float64(r.GeneratedTokens)
		}
		res.Runs = append(res.Runs, r)
		res.GeneratedTokens += r.GeneratedTokens
		energy = append(energy, basis)
		jpt = append(jpt, r.JPerToken)
		if progress != nil {
			progress(i + 1)
		}
	}
	res.EndedAt = time.Now()
	var err error
	if res.Energy, err = stats.Summarize(energy); err != nil {
		return res, err
	}
	if res.JPerToken, err = stats.Summarize(jpt); err != nil {
		return res, err
	}
	if res.GeneratedTokens == 0 {
		return res, errors.New("the backend generated no tokens")
	}
	return res, nil
}
