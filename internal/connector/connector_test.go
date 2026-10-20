// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package connector

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MarioUrenaGarcia/greenlens/internal/discovery"
)

func fixture(t *testing.T, kind, version, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "connectors", kind, version, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// serve answers each path with a fixture file; other paths get 404.
func serve(t *testing.T, routes map[string][]byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		if b, ok := routes[key]; ok {
			_, _ = w.Write(b)
			return
		}
		if b, ok := routes["400 "+key]; ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestVLLM(t *testing.T) {
	url := serve(t, map[string][]byte{"/metrics": fixture(t, "vllm", "docs", "metrics.txt"), "/version": fixture(t, "vllm", "docs", "version.json")})
	s, err := Scrape(context.Background(), Client(), discovery.VLLM, url)
	if err != nil {
		t.Fatal(err)
	}
	want := Counters{Model: "Qwen/Qwen2.5-0.5B-Instruct", PromptTokens: 1500, GeneratedTokens: 4200, Requests: 32}
	if s.Version != "0.11.0" || len(s.Models) != 1 || s.Models[0] != want || s.Active != 2 || !s.TokenCounters {
		t.Fatalf("snapshot %+v", s)
	}
}

func TestSGLang(t *testing.T) {
	url := serve(t, map[string][]byte{"/metrics": fixture(t, "sglang", "docs", "metrics.txt")})
	s, err := Scrape(context.Background(), Client(), discovery.SGLang, url)
	if err != nil || len(s.Models) != 1 || s.Models[0].GeneratedTokens != 7557572 || s.Active != 3 {
		t.Fatalf("snapshot %+v err %v", s, err)
	}
}

func TestLlamaCpp(t *testing.T) {
	url := serve(t, map[string][]byte{"/metrics": fixture(t, "llamacpp", "docs", "metrics.txt"), "/props": fixture(t, "llamacpp", "docs", "props.json")})
	s, err := Scrape(context.Background(), Client(), discovery.LlamaCpp, url)
	if err != nil || len(s.Models) != 1 || s.Models[0].Model != "qwen2.5-0.5b-instruct-q4_k_m.gguf" || s.Models[0].PromptTokens != 310 || s.Models[0].GeneratedTokens != 1280 {
		t.Fatalf("snapshot %+v err %v", s, err)
	}
	// Without --metrics the server answers 501.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotImplemented) }))
	defer srv.Close()
	if _, err := Scrape(context.Background(), Client(), discovery.LlamaCpp, srv.URL); !errors.Is(err, ErrNoMetrics) {
		t.Fatalf("err %v, want ErrNoMetrics", err)
	}
}

func TestLlamaCppRouterMode(t *testing.T) {
	m := fixture(t, "llamacpp", "docs", "metrics.txt")
	url := serve(t, map[string][]byte{
		"400 /metrics":       []byte("model name is missing from the request"),
		"/v1/models":         []byte(`{"data":[{"id":"a"},{"id":"b c"}]}`),
		"/metrics?model=a":   m,
		"/metrics?model=b+c": m,
	})
	s, err := Scrape(context.Background(), Client(), discovery.LlamaCpp, url)
	if err != nil || len(s.Models) != 2 || s.Active != 2 || !slices.Equal(s.Loaded, []string{"a", "b c"}) {
		t.Fatalf("snapshot %+v err %v", s, err)
	}
}

func TestTGI(t *testing.T) {
	m := fixture(t, "tgi", "docs", "metrics.txt")
	url := serve(t, map[string][]byte{"/info": fixture(t, "tgi", "docs", "info.json"), "/metrics": m})
	s, err := Scrape(context.Background(), Client(), discovery.TGI, url)
	want := Counters{Model: "Qwen/Qwen2.5-0.5B-Instruct", PromptTokens: 960, GeneratedTokens: 2400, Requests: 12}
	if err != nil || s.Version != "3.3.7" || len(s.Models) != 1 || s.Models[0] != want {
		t.Fatalf("snapshot %+v err %v", s, err)
	}
}

func TestOllama(t *testing.T) {
	url := serve(t, map[string][]byte{
		"/api/version": fixture(t, "ollama", "0.40.6", "api_version.json"),
		"/api/ps":      fixture(t, "ollama", "0.40.6", "api_ps.json"),
	})
	s, err := Scrape(context.Background(), Client(), discovery.Ollama, url)
	if err != nil || s.Version != "0.40.6" || s.TokenCounters || len(s.Loaded) != 0 {
		t.Fatalf("snapshot %+v err %v", s, err)
	}
}

func TestOpenAIAndLimits(t *testing.T) {
	url := serve(t, map[string][]byte{"/v1/models": []byte(`{"object":"list","data":[{"id":"gpt-oss"}]}`)})
	s, err := Scrape(context.Background(), Client(), discovery.OpenAI, url)
	if err != nil || !slices.Equal(s.Loaded, []string{"gpt-oss"}) {
		t.Fatalf("snapshot %+v err %v", s, err)
	}
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", MaxBody+10)))
	}))
	defer big.Close()
	if _, err := Scrape(context.Background(), Client(), discovery.VLLM, big.URL); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("oversized response: %v", err)
	}
	redirect := httptest.NewServer(http.RedirectHandler("http://192.0.2.1/metrics", http.StatusFound))
	defer redirect.Close()
	if _, err := Scrape(context.Background(), Client(), discovery.VLLM, redirect.URL); err == nil || !strings.Contains(err.Error(), "another host") {
		t.Fatalf("redirect: %v", err)
	}
	if _, err := Scrape(context.Background(), Client(), "bogus", url); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

// Two builds of llama-server recorded serving qwen2.5-0.5b-instruct
// after two chat requests.
func TestLlamaCppRecorded(t *testing.T) {
	for _, build := range []string{"b11669", "b10416"} {
		url := serve(t, map[string][]byte{"/metrics": fixture(t, "llamacpp", build, "metrics.txt"), "/props": fixture(t, "llamacpp", build, "props.json")})
		s, err := Scrape(context.Background(), Client(), discovery.LlamaCpp, url)
		if err != nil {
			t.Fatalf("%s: %v", build, err)
		}
		want := Counters{Model: "qwen2.5-0.5b-instruct-q4_k_m.gguf", PromptTokens: 42, GeneratedTokens: 19}
		if len(s.Models) != 1 || s.Models[0] != want || s.Active != 0 || !s.TokenCounters || !slices.Equal(s.Loaded, []string{want.Model}) {
			t.Fatalf("%s: snapshot %+v", build, s)
		}
		// Without --metrics both builds answer 501 with an explanation.
		body := fixture(t, "llamacpp", build, "metrics_disabled.txt")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = w.Write(body)
		}))
		_, err = Scrape(context.Background(), Client(), discovery.LlamaCpp, srv.URL)
		srv.Close()
		if !errors.Is(err, ErrNoMetrics) {
			t.Fatalf("%s: %v", build, err)
		}
	}
}

func TestOllamaRecordedWithAModel(t *testing.T) {
	url := serve(t, map[string][]byte{"/api/version": fixture(t, "ollama", "0.40.6", "api_version.json"), "/api/ps": fixture(t, "ollama", "0.40.6", "api_ps_loaded.json")})
	s, err := Scrape(context.Background(), Client(), discovery.Ollama, url)
	if err != nil || s.Version != "0.40.6" || !slices.Equal(s.Loaded, []string{"qwen2.5:0.5b"}) || s.TokenCounters {
		t.Fatalf("snapshot %+v err %v", s, err)
	}
}
