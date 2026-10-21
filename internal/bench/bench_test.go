// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package bench

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/discovery"
	"github.com/MarioUrenaGarcia/greenlens/internal/stats"
)

func TestPrompts(t *testing.T) {
	p := DefaultPrompts()
	if len(p) != 8 || Hash(p) != Hash(DefaultPrompts()) || len(Hash(p)) != 64 {
		t.Fatalf("%d prompts, hash %s", len(p), Hash(p))
	}
	q, err := ParsePrompts([]byte("one\n\n  two  \n"))
	if err != nil || len(q) != 2 || q[1] != "two" {
		t.Fatalf("%q %v", q, err)
	}
	if Hash(q) == Hash([]string{"one", "two "}) {
		t.Error("different prompts share a hash")
	}
	if _, err := ParsePrompts([]byte("\n \n")); err == nil {
		t.Error("an empty set was accepted")
	}
}

// server answers as Ollama or an OpenAI-compatible server, checks the
// generation is deterministic, and generates 10 tokens per prompt.
func server(t *testing.T, kind discovery.Kind) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req["model"] != "m" || req["stream"] != false {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch kind {
		case discovery.Ollama:
			opts, _ := req["options"].(map[string]any)
			if r.URL.Path != "/api/generate" || opts["temperature"] != 0.0 || opts["seed"] != 42.0 || opts["num_predict"] != 128.0 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, `{"model":"m","response":"x","done":true,"prompt_eval_count":20,"eval_count":10}`)
		default:
			if r.URL.Path != "/v1/chat/completions" || req["temperature"] != 0.0 || req["max_tokens"] != 128.0 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, `{"model":"m","choices":[{"message":{"content":"x"}}],"usage":{"prompt_tokens":20,"completion_tokens":10}}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// meter reports 100 J per run plus a little noise, 30 J of it idle.
func meter(noise []float64) Meter {
	i := 0
	return func(fn func() error) (Energy, time.Duration, error) {
		if err := fn(); err != nil {
			return Energy{}, 0, err
		}
		e := Energy{TotalJ: 100 + noise[i%len(noise)], IdleJ: 30, BackendJ: 50 + noise[i%len(noise)]/2}
		i++
		return e, 2 * time.Second, nil
	}
}

func TestBench(t *testing.T) {
	for _, kind := range []discovery.Kind{discovery.Ollama, discovery.VLLM} {
		srv, calls := server(t, kind)
		target := Target{Kind: kind, Endpoint: srv.URL, Model: "m"}
		prompts := DefaultPrompts()
		r, err := Bench(context.Background(), srv.Client(), target, quick(), prompts, meter([]float64{0, 1, -1, 0.5, -0.5}), BasisDynamic, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		// One warmup pass and five measured ones.
		if calls.Load() != int64(6*len(prompts)) || len(r.Runs) != 5 || r.GeneratedTokens != 5*80 {
			t.Fatalf("%s: %d calls, %+v", kind, calls.Load(), r)
		}
		run := r.Runs[0]
		if run.PromptTokens != 160 || run.GeneratedTokens != 80 || run.DynamicJ != 70 || math.Abs(run.JPerToken-70.0/80) > 1e-12 || r.Basis != BasisDynamic {
			t.Fatalf("%s: run %+v", kind, run)
		}
		if math.Abs(r.Energy.Mean-70) > 1e-9 || r.JPerToken.CV > 0.05 || r.JPerToken.CI95 <= 0 || r.PromptSetSHA256 != Hash(prompts) {
			t.Fatalf("%s: summary %+v %+v", kind, r.Energy, r.JPerToken)
		}
	}
	// On the total energy, and on the energy of the backend.
	srv, _ := server(t, discovery.Ollama)
	r, err := Bench(context.Background(), srv.Client(), Target{Kind: discovery.Ollama, Endpoint: srv.URL, Model: "m"}, quick(), []string{"a"}, meter([]float64{0, 2}), BasisTotal, false, nil)
	if err != nil || r.Basis != BasisTotal || r.Calibrated || math.Abs(r.Runs[0].JPerToken-10) > 1e-12 {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = Bench(context.Background(), srv.Client(), Target{Kind: discovery.Ollama, Endpoint: srv.URL, Model: "m"}, quick(), []string{"a"}, meter([]float64{0, 2}), BasisBackend, true, nil)
	if err != nil || r.Basis != BasisBackend || r.Runs[0].BackendJ != 50 || math.Abs(r.Runs[0].JPerToken-5) > 1e-12 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestBenchErrors(t *testing.T) {
	srv, _ := server(t, discovery.Ollama)
	// The backend does not know the model.
	_, err := Bench(context.Background(), srv.Client(), Target{Kind: discovery.Ollama, Endpoint: srv.URL, Model: "missing"}, quick(), []string{"a"}, meter([]float64{0}), BasisDynamic, true, nil)
	var re *RequestError
	if !errors.As(err, &re) || re.Status != http.StatusBadRequest {
		t.Fatalf("%v", err)
	}
	p := quick()
	p.Runs = 1
	if _, err := Bench(context.Background(), srv.Client(), Target{Kind: discovery.Ollama, Endpoint: srv.URL, Model: "m"}, p, []string{"a"}, meter([]float64{0}), BasisDynamic, true, nil); err == nil {
		t.Fatal("one run was accepted")
	}
}

func TestDiagnose(t *testing.T) {
	yes := true
	r := Result{JPerToken: statsCV(0.2), Context: Context{Governor: "powersave", StartTempC: 50, EndTempC: 70, Throttled: &yes, OtherCPUShare: 0.4}}
	got := strings.Join(Diagnose(r), ",")
	if got != "governor,temperature,throttling,other_processes" {
		t.Fatalf("%s", got)
	}
	r.JPerToken = statsCV(0.04)
	if Diagnose(r) != nil {
		t.Fatal("stable runs have no causes")
	}
}

func TestContext(t *testing.T) {
	fsys := fstest.MapFS{
		"proc/cpuinfo":                                                        {Data: []byte("processor\t: 0\nvendor_id\t: AuthenticAMD\nmodel name\t: AMD Ryzen 9 7900X 12-Core Processor\n\n")},
		"proc/sys/kernel/osrelease":                                           {Data: []byte("7.0.0-38-generic\n")},
		"proc/driver/nvidia/version":                                          {Data: []byte("NVRM version: NVIDIA UNIX Open Kernel Module for x86_64  595.99.02  Release Build  (builder)  Wed Aug 26 2026\n")},
		"sys/devices/system/cpu/online":                                       {Data: []byte("0-1\n")},
		"sys/devices/system/cpu/cpu0/cpufreq/scaling_governor":                {Data: []byte("performance\n")},
		"sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq":                {Data: []byte("4700000\n")},
		"sys/devices/system/cpu/cpu0/thermal_throttle/core_throttle_count":    {Data: []byte("2\n")},
		"sys/devices/system/cpu/cpu1/thermal_throttle/package_throttle_count": {Data: []byte("1\n")},
	}
	c := Snapshot(fsys)
	if c.CPU != "AMD Ryzen 9 7900X 12-Core Processor" || c.Kernel != "7.0.0-38-generic" || c.NVIDIADriver != "595.99.02" || c.Governor != "performance" || c.FrequencyMHz != 4700 {
		t.Fatalf("%+v", c)
	}
	if n, ok := Throttles(fsys); !ok || n != 3 {
		t.Fatalf("%d %v", n, ok)
	}
	if _, ok := Throttles(fstest.MapFS{"sys/devices/system/cpu/online": {Data: []byte("0\n")}}); ok {
		t.Fatal("no counters must be reported")
	}
}

func statsCV(cv float64) stats.Summary { return stats.Summary{N: 5, Mean: 1, CV: cv} }

// Generate reads the counts of real responses: Ollama 0.40.6 and
// llama-server b11669 for the same prompt.
func TestGenerateRecorded(t *testing.T) {
	for _, c := range []struct {
		kind discovery.Kind
		path string
	}{
		{discovery.Ollama, "ollama/0.40.6/generate.json"},
		{discovery.LlamaCpp, "llamacpp/b11669/chat.json"},
	} {
		body, err := os.ReadFile(filepath.Join("..", "..", "testdata", "connectors", c.path))
		if err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
		tok, err := Generate(context.Background(), srv.Client(), Target{Kind: c.kind, Endpoint: srv.URL, Model: "m"}, quick(), "p")
		srv.Close()
		if err != nil || tok.Prompt != 34 || tok.Generated != 7 {
			t.Errorf("%s: %+v %v", c.path, tok, err)
		}
	}
}

// quick are the default parameters without the warmup time and minimum
// run length, which would make the tests last minutes.
func quick() Params {
	p := DefaultParams()
	p.WarmupS, p.MinRunS = 0, 0
	return p
}

func TestRunsLastTheMinimum(t *testing.T) {
	srv, calls := server(t, discovery.Ollama)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		srv.Config.Handler.ServeHTTP(w, r)
	}))
	defer slow.Close()
	p := quick()
	p.MinRunS = 0.05
	r, err := Bench(context.Background(), slow.Client(), Target{Kind: discovery.Ollama, Endpoint: slow.URL, Model: "m"}, p, []string{"a"}, meter([]float64{0}), BasisTotal, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A pass takes a little over 10 ms, so about five make 50 ms; every
	// run makes the same number.
	if r.Params.Passes < 3 || r.Params.Passes > 6 || calls.Load() != int64(1+5*r.Params.Passes) {
		t.Fatalf("%d passes, %d calls", r.Params.Passes, calls.Load())
	}
	for _, run := range r.Runs {
		if run.GeneratedTokens != int64(10*r.Params.Passes) {
			t.Fatalf("run %+v", run)
		}
	}
}
