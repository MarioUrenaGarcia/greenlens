// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens"
	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/apispec"
	"github.com/MarioUrenaGarcia/greenlens/internal/bench"
	"github.com/MarioUrenaGarcia/greenlens/internal/calibrate"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/stats"
)

func loadSpec(t *testing.T) *apispec.Spec {
	t.Helper()
	s, err := apispec.Load(greenlens.OpenAPI)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestRoutesMatchTheSpec checks that the description and the agent list the
// same operations, with the same roles.
func TestRoutesMatchTheSpec(t *testing.T) {
	a := &Agent{}
	type served struct {
		role       string
		socketOnly bool
	}
	routes := map[string]served{"GET " + api.PathMetrics: {role: "public"}}
	for _, rt := range append(a.routes(), a.httpRoutes(false)...) {
		role := string(rt.role)
		if role == "" {
			role = "public"
		}
		routes[rt.pattern] = served{role, rt.socketOnly}
	}
	documented := map[string]bool{}
	for _, op := range loadSpec(t).Operations() {
		key := op.Method + " " + op.Path
		documented[key] = true
		got, ok := routes[key]
		switch {
		case !ok:
			t.Errorf("%s is described but not served", key)
		case got.role != op.Role:
			t.Errorf("%s: the description says %s, the agent requires %s", key, op.Role, got.role)
		case got.socketOnly != op.SocketOnly:
			t.Errorf("%s: socket only %v in the description, %v in the agent", key, op.SocketOnly, got.socketOnly)
		}
	}
	for key := range routes {
		if !documented[key] {
			t.Errorf("%s is served but not described", key)
		}
	}
}

// TestResponsesMatchTheSpec calls every operation and checks each answer
// against its status and schema in the description.
func TestResponsesMatchTheSpec(t *testing.T) {
	spec := loadSpec(t)
	ops := map[string]apispec.Operation{}
	for _, op := range spec.Operations() {
		ops[op.ID] = op
	}
	listen := freePort(t)
	r := startAgentWith(t, "", func(c *config.Config) {
		c.Server.Enabled = true
		c.Server.Listen = listen
	})
	waitSamples(t, r, 3)
	sock := call{t: t, c: socketClient(r.socket), base: "http://agent"}
	anon := call{t: t, c: &http.Client{CheckRedirect: noRedirect}, base: "http://" + listen}
	called := map[string]bool{}
	check := func(id string, resp reply, body []byte) {
		t.Helper()
		op, ok := ops[id]
		if !ok {
			t.Fatalf("no operation %s", id)
		}
		called[id] = true
		status := strconv.Itoa(resp.code)
		if !op.HasStatus(status) {
			t.Errorf("%s answered %s, which is not described: %s", id, status, body)
			return
		}
		schema, ok := spec.ResponseSchema(op, status)
		if !ok {
			return
		}
		if err := spec.Validate(schema, body); err != nil {
			t.Errorf("%s %s: %v\n%s", id, status, err, body)
		}
	}
	get := func(id string, c call, path string) []byte {
		t.Helper()
		resp, body := c.do("GET", path, nil)
		check(id, resp, body)
		return body
	}
	post := func(id string, c call, method, path string, in any, extra ...string) []byte {
		t.Helper()
		resp, body := c.do(method, path, in, extra...)
		check(id, resp, body)
		return body
	}

	get("health", anon, api.PathHealth)
	get("ready", anon, api.PathReady)
	get("metrics", anon, api.PathMetrics)
	if body := get("openapi", anon, api.PathOpenAPI); !strings.HasPrefix(string(body), "# SPDX") {
		t.Error("the description is not served")
	}
	get("status", sock, api.PathStatus)
	get("status", anon, api.PathStatus)
	var cat api.Catalog
	if err := json.Unmarshal(get("catalog", anon, api.PathCatalogs+"/es"), &cat); err != nil || cat.Messages["domain.package"] == "" || cat.Messages["cli.root.short"] != "" {
		t.Errorf("catalog: %v %d messages", err, len(cat.Messages))
	}
	get("catalog", anon, api.PathCatalogs+"/xx")
	get("about", sock, api.PathAbout)
	get("sources", sock, api.PathSources)
	get("backends", sock, api.PathBackends)
	get("live", sock, api.PathLive)
	stream, cancel := openStream(t, r, 0)
	readEvents(t, stream.Body, func(e sse) bool { return e.name == eventBackends })
	cancel()
	_ = stream.Body.Close()
	called["stream"] = true
	now := time.Now().UTC()
	q := url.Values{"from": {now.Add(-time.Hour).Format(time.RFC3339)}, "to": {now.Add(time.Minute).Format(time.RFC3339)}}
	get("usage", sock, api.PathUsage+"?"+q.Encode())
	q.Set("bucket", "1m")
	get("usage", sock, api.PathUsage+"?"+q.Encode())
	q.Set("group_by", "model")
	get("usage", sock, api.PathUsage+"?"+q.Encode())
	q.Del("bucket")
	get("usage", sock, api.PathUsage+"?"+q.Encode())
	get("usage", sock, api.PathUsage+"?from=yesterday")

	cal := calibrate.Result{StartedAt: now.Add(-time.Minute), Duration: time.Minute, Samples: 60,
		Domains: map[source.Domain]calibrate.Stats{source.DomainPackage: {P10: 1, P50: 2, P90: 3}},
		Quality: map[source.Domain]string{source.DomainPackage: "measured"}}
	post("saveCalibration", sock, "POST", api.PathCalibrations, cal)
	post("saveCalibration", sock, "POST", api.PathCalibrations, map[string]any{"samples": 0})
	get("status", sock, api.PathStatus)

	sum := stats.Summary{N: 2, Mean: 1, StdDev: 0.1, CV: 0.1, CI95: 0.9}
	b := bench.Result{StartedAt: now.Add(-time.Minute), EndedAt: now, Target: bench.Target{Kind: "ollama", Endpoint: "http://127.0.0.1:11434", Model: "qwen2.5:0.5b"},
		Params: bench.Params{MaxTokens: 64, Seed: 1, Runs: 2}, PromptSetSHA256: strings.Repeat("a", 64), Prompts: 3, Basis: "backend", Calibrated: true,
		Runs:   []bench.Run{{EnergyJ: 10, DurationS: 1, GeneratedTokens: 10, JPerToken: 1}, {EnergyJ: 11, DurationS: 1, GeneratedTokens: 10, JPerToken: 1.1}},
		Energy: sum, JPerToken: sum, GeneratedTokens: 20}
	var saved bench.Result
	if err := json.Unmarshal(post("saveBenchmark", sock, "POST", api.PathBenchResults, b), &saved); err != nil || saved.ID == 0 {
		t.Fatalf("benchmark not saved: %v", err)
	}
	post("saveBenchmark", sock, "POST", api.PathBenchResults, map[string]any{"runs": []any{}})

	// The agent runs a benchmark of its own against a fake server.
	up := benchServer(t)
	get("benchmarkRun", sock, api.PathBenchRun)
	post("cancelBenchmark", sock, "DELETE", api.PathBenchRun, nil)
	post("startBenchmark", sock, "POST", api.PathBenchmarks, api.BenchmarkRequest{Kind: "ollama", Model: "qwen2.5:0.5b"})
	req := api.BenchmarkRequest{Kind: "ollama", Endpoint: up.URL, Model: "qwen2.5:0.5b", Runs: 2, MaxTokens: 16}
	post("startBenchmark", sock, "POST", api.PathBenchmarks, req)
	post("startBenchmark", sock, "POST", api.PathBenchmarks, req)
	run := waitBenchmark(t, sock)
	if run.State != "done" || run.BenchmarkID == 0 {
		t.Fatalf("benchmark run %+v", run)
	}
	get("benchmarkRun", sock, api.PathBenchRun)
	get("benchmarks", sock, api.PathBenchmarks)
	get("benchmarks", sock, api.PathBenchmarks+"?limit=0")
	get("benchmark", sock, api.PathBenchmarks+"/"+strconv.FormatInt(saved.ID, 10))
	get("benchmark", sock, api.PathBenchmarks+"/999")
	get("benchmark", sock, api.PathBenchmarks+"/x")

	if resp, body := sock.do("GET", api.PathBackup, nil); resp.code != 200 || !strings.HasPrefix(string(body), "SQLite format 3") {
		t.Errorf("backup: %d", resp.code)
	} else {
		called["backup"] = true
	}

	var tok api.CreatedToken
	if err := json.Unmarshal(post("createToken", sock, "POST", api.PathTokens, api.CreateToken{Name: "viewer", Role: "viewer"}), &tok); err != nil {
		t.Fatal(err)
	}
	post("createToken", sock, "POST", api.PathTokens, api.CreateToken{Name: "x", Role: "root"})
	get("tokens", sock, api.PathTokens)
	viewer := anon
	viewer.header = http.Header{"Authorization": {"Bearer " + tok.Secret}}
	post("saveCalibration", viewer, "POST", api.PathCalibrations, cal)
	get("tokens", viewer, api.PathTokens)

	get("session", anon, api.PathSession)
	get("session", sock, api.PathSession)
	post("signIn", anon, "POST", api.PathSession, api.Login{Token: "greenlens_nope"})
	post("signIn", anon, "POST", api.PathSession, "not an object")
	post("signIn", anon, "POST", api.PathSession, api.Login{Token: tok.Secret})
	post("signOut", anon, "DELETE", api.PathSession, nil)

	var code api.SessionCode
	if err := json.Unmarshal(post("sessionCode", sock, "POST", api.PathSessionCodes, api.SessionCodeRequest{}), &code); err != nil {
		t.Fatal(err)
	}
	post("sessionCode", sock, "POST", api.PathSessionCodes, api.SessionCodeRequest{Role: "root"})
	resp, body := anon.do("GET", strings.TrimPrefix(code.URL, "http://"+listen), nil)
	check("signInWithCode", resp, body)

	post("revokeToken", sock, "DELETE", api.PathTokens+"/viewer", nil)
	post("revokeToken", sock, "DELETE", api.PathTokens+"/viewer", nil)
	get("config", sock, api.PathConfig)
	post("previewConfig", sock, "POST", api.PathConfigCheck, api.ConfigChange{Changes: map[string]string{"indicators.pue": "1.2"}})
	post("previewConfig", sock, "POST", api.PathConfigCheck, api.ConfigChange{Changes: map[string]string{"indicators.pue": "lots"}})
	post("previewConfig", sock, "POST", api.PathConfigCheck, "not an object")
	post("applyConfig", sock, "PUT", api.PathConfig, api.ConfigChange{Changes: map[string]string{"indicators.pue": "1.2"}})
	get("audit", sock, api.PathAudit)
	get("audit", sock, api.PathAudit+"?limit=5000")

	for id := range ops {
		if !called[id] {
			t.Errorf("%s was never called", id)
		}
	}
}
