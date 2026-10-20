// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Responses as documented: Ollama's docs/api.md and the OpenAI chat
// completions API, with their text replaced.
const (
	ollamaStream = `{"model":"qwen2.5:0.5b","created_at":"2026-10-20T12:00:00Z","response":"a","done":false}
{"model":"qwen2.5:0.5b","created_at":"2026-10-20T12:00:00Z","response":"b","done":false}
{"model":"qwen2.5:0.5b","created_at":"2026-10-20T12:00:00Z","response":"c","done":false}
{"model":"qwen2.5:0.5b","created_at":"2026-10-20T12:00:01Z","response":"","done":true,"done_reason":"stop","total_duration":1200000000,"prompt_eval_count":26,"eval_count":3}
`
	ollamaJSON = `{"model":"llama3.2","created_at":"2026-10-20T12:00:00Z","message":{"role":"assistant","content":"x"},"done":true,"prompt_eval_count":12,"eval_count":40}`
	openAIJSON = `{
  "id": "chatcmpl-1",
  "object": "chat.completion",
  "model": "qwen2.5-0.5b-instruct",
  "choices": [{"index": 0, "message": {"role": "assistant", "content": "x"}, "finish_reason": "stop"}],
  "usage": {"prompt_tokens": 9, "completion_tokens": 12, "total_tokens": 21}
}`
	openAISSE = "data: {\"id\":\"c\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a\"}}]}\n\n" +
		"data: {\"id\":\"c\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"b\"}}]}\n\n" +
		"data: {\"id\":\"c\",\"model\":\"m\",\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\n" +
		"data: [DONE]\n\n"
	openAISSENoUsage = "data: {\"model\":\"m\",\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n" +
		"data: {\"model\":\"m\",\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\n\n: keep-alive\n\ndata: [DONE]\n\n"
)

func tallyOf(lines ...string) *tally {
	t := &tally{}
	for _, l := range lines {
		t.line([]byte(l))
	}
	return t
}

func TestTally(t *testing.T) {
	for _, c := range []struct {
		name, body, model string
		prompt, gen       int64
		whole             bool
	}{
		{"ollama stream", ollamaStream, "qwen2.5:0.5b", 26, 3, false},
		{"ollama json", ollamaJSON, "llama3.2", 12, 40, true},
		{"openai json", openAIJSON, "qwen2.5-0.5b-instruct", 9, 12, true},
		{"openai sse", openAISSE, "m", 5, 2, false},
		{"openai sse without usage", openAISSENoUsage, "m", 0, 2, false},
	} {
		var tl *tally
		if c.whole {
			tl = tallyOf(c.body)
		} else {
			tl = tallyOf(strings.Split(c.body, "\n")...)
		}
		p, g := tl.counts()
		if !tl.seen || tl.model != c.model || p != c.prompt || g != c.gen {
			t.Errorf("%s: model %q, %d/%d", c.name, tl.model, p, g)
		}
	}
	if tl := tallyOf(`{"models":[{"name":"x"}]}`, "not json", "data: [DONE]"); tl.seen {
		t.Error("a response without tokens was counted")
	}
}

func TestMeter(t *testing.T) {
	m := NewMeter()
	at := time.Unix(1000, 0)
	m.now = func() time.Time { return at }
	a := m.Begin("ollama")
	b := m.Begin("ollama")
	other := m.Begin("vllm")
	at = at.Add(time.Second)
	// a streamed 3 pieces and b one: 30 J split 3:1.
	m.Piece(a)
	m.Piece(a)
	m.Piece(a)
	m.Piece(b)
	m.Tick(at, time.Second, map[string]float64{"ollama": 30, "vllm": 8})
	// Nothing streamed while prompts were processed: equal parts.
	at = at.Add(time.Second)
	m.Tick(at, time.Second, map[string]float64{"ollama": 10})
	// Half a second later a ends; the last sample ran at 10 W, shared
	// equally.
	at = at.Add(500 * time.Millisecond)
	if e := m.End(a); math.Abs(e-(22.5+5+2.5)) > 1e-9 {
		t.Fatalf("a %v", e)
	}
	if e := m.End(other); math.Abs(e-8) > 1e-9 {
		t.Fatalf("other %v", e)
	}
	// A request that starts mid-sample gets the part it ran for.
	c := m.Begin("ollama")
	at = at.Add(500 * time.Millisecond)
	m.Tick(at, time.Second, map[string]float64{"ollama": 20})
	if e := m.End(c); math.Abs(e-5) > 1e-9 {
		t.Fatalf("c %v", e)
	}
	if e := m.End(b); e <= 0 {
		t.Fatalf("b %v", e)
	}
}

type registered struct {
	mu   sync.Mutex
	list map[string]string
}

func (r *registered) add(name, h string) {
	r.mu.Lock()
	r.list[name] = h
	r.mu.Unlock()
}

func newProxy(t *testing.T, upstream http.HandlerFunc, energy bool, logs *bytes.Buffer, reg *registered) (*Proxy, *httptest.Server, *Meter) {
	t.Helper()
	up := httptest.NewServer(upstream)
	t.Cleanup(up.Close)
	u, err := url.Parse(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	m := NewMeter()
	key := sha256.Sum256([]byte("Bearer team-a-key"))
	cons := NewConsumers("Authorization", bytes.Repeat([]byte{7}, saltSize), map[string]string{hex.EncodeToString(key[:]): "team-a"}, reg.add)
	p := New(Options{Upstream: u, Backend: func() string { return "ollama-11434" }, Meter: m, EnergyHeader: energy,
		ConsumerHeader: "Authorization", Consumers: cons, Log: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	front := httptest.NewServer(p)
	t.Cleanup(front.Close)
	return p, front, m
}

func ollamaUpstream(w http.ResponseWriter, r *http.Request) {
	// The request body reaches the upstream unchanged.
	body, _ := io.ReadAll(r.Body)
	if !bytes.Contains(body, []byte(`"prompt"`)) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	switch r.URL.Path {
	case "/api/generate":
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, l := range strings.SplitAfter(ollamaStream, "\n") {
			_, _ = io.WriteString(w, l)
			w.(http.Flusher).Flush()
		}
	case "/api/chat":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = io.WriteString(w, ollamaJSON)
	}
}

func TestStreamPassesThroughWithTrailer(t *testing.T) {
	var logs bytes.Buffer
	reg := &registered{list: map[string]string{}}
	p, front, _ := newProxy(t, ollamaUpstream, true, &logs, reg)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, front.URL+"/api/generate", strings.NewReader(`{"model":"qwen2.5:0.5b","prompt":"hi"}`))
	req.Header.Set("Authorization", "Bearer team-a-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(body) != ollamaStream {
		t.Fatalf("body changed: %q %v", body, err)
	}
	if v := resp.Trailer.Get(EnergyHeader); v == "" || len(resp.Trailer) != 1 {
		t.Fatalf("energy trailer: %v", resp.Trailer)
	}
	cs := p.Counters("ollama-11434")
	if len(cs) != 1 || cs[0].Model != "qwen2.5:0.5b" || cs[0].Consumer != "team-a" || cs[0].PromptTokens != 26 || cs[0].GeneratedTokens != 3 || cs[0].Requests != 1 {
		t.Fatalf("counters %+v", cs)
	}
	if reg.list["team-a"] != "" || len(reg.list) != 1 {
		t.Fatalf("an aliased consumer is recorded without HMAC: %v", reg.list)
	}
}

func TestJSONGetsTheEnergyHeader(t *testing.T) {
	var logs bytes.Buffer
	reg := &registered{list: map[string]string{}}
	p, front, _ := newProxy(t, ollamaUpstream, true, &logs, reg)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, front.URL+"/api/chat", strings.NewReader(`{"model":"llama3.2","prompt":"hi","stream":false}`))
	req.Header.Set("Authorization", "Bearer someone-else")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != ollamaJSON || resp.Header.Get(EnergyHeader) == "" || resp.ContentLength != int64(len(ollamaJSON)) {
		t.Fatalf("header %q, length %d, body %q", resp.Header.Get(EnergyHeader), resp.ContentLength, body)
	}
	cs := p.Counters("ollama-11434")
	if len(cs) != 1 || !strings.HasPrefix(cs[0].Consumer, "consumer-") || cs[0].GeneratedTokens != 40 {
		t.Fatalf("counters %+v", cs)
	}
	// An unknown consumer is kept by the HMAC of its key, never the key.
	h := reg.list[cs[0].Consumer]
	if len(h) != 64 || strings.Contains(h, "someone-else") || h[:12] != strings.TrimPrefix(cs[0].Consumer, "consumer-") {
		t.Fatalf("registered %v", reg.list)
	}
}

func TestUpstreamDown(t *testing.T) {
	var logs bytes.Buffer
	reg := &registered{list: map[string]string{}}
	_, front, m := newProxy(t, ollamaUpstream, false, &logs, reg)
	// Point the proxy at a closed port.
	dead := httptest.NewServer(http.NotFoundHandler())
	u, _ := url.Parse(dead.URL)
	dead.Close()
	p := New(Options{Upstream: u, Backend: func() string { return "x" }, Meter: m, Log: slog.New(slog.NewTextHandler(&logs, nil))})
	front.Config.Handler = p
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, front.URL+"/api/generate", strings.NewReader(`{"prompt":"PRIVATE-TEXT"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if strings.Contains(logs.String(), "PRIVATE-TEXT") || !strings.Contains(logs.String(), "could not reach the upstream") {
		t.Fatalf("logs %q", logs.String())
	}
	m.mu.Lock()
	n := len(m.inflight)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d requests left in flight", n)
	}
}

func TestSaltAndAliases(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, SaltFile)
	a, err := LoadSalt(p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadSalt(p)
	if err != nil || !bytes.Equal(a, b) || len(a) != saltSize {
		t.Fatalf("salt changed: %v", err)
	}
	if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("salt mode %v %v", st.Mode(), err)
	}
	f := filepath.Join(dir, "aliases.yaml")
	sum := sha256.Sum256([]byte("k"))
	if err := os.WriteFile(f, []byte("team-a: sha256:"+hex.EncodeToString(sum[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := LoadAliases(f)
	if err != nil || m[hex.EncodeToString(sum[:])] != "team-a" {
		t.Fatalf("%v %v", m, err)
	}
	for _, bad := range []string{"team-a: k\n", "team a: sha256:" + hex.EncodeToString(sum[:]) + "\n", "- x\n"} {
		if err := os.WriteFile(f, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadAliases(f); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
