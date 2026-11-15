// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package hub

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/auth"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/exporter/prometheus"
	"github.com/MarioUrenaGarcia/greenlens/internal/pki"
	"github.com/MarioUrenaGarcia/greenlens/internal/promtext"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

// authority is a fleet authority with the directory of what it issued.
type authority struct {
	t     *testing.T
	dir   string
	out   string
	roots *x509.CertPool
}

func newAuthority(t *testing.T) *authority {
	t.Helper()
	a := &authority{t: t, dir: filepath.Join(t.TempDir(), "ca"), out: t.TempDir(), roots: x509.NewCertPool()}
	ca, err := pki.InitCA(a.dir, "fleet", 30, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	a.roots.AddCert(ca)
	return a
}

func (a *authority) issue(name, role string) pki.Issued {
	a.t.Helper()
	is, err := pki.Issue(a.dir, a.out, pki.Request{Name: name, Role: role, Hosts: []string{"127.0.0.1"}, Days: 7}, time.Now())
	if err != nil {
		a.t.Fatal(err)
	}
	return is
}

// agentStub serves metrics as an agent of the fleet does: with its
// certificate, and only to clients with a certificate of the authority.
type agentStub struct {
	mu      sync.Mutex
	pkgJ    float64
	gpu     float64
	powerW  float64
	down    bool
	srv     *httptest.Server
	clients []string
}

func (s *agentStub) set(pkg, gpu, power float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pkgJ, s.gpu, s.powerW = pkg, gpu, power
}

func (s *agentStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients = append(s.clients, pki.Role(r.TLS.PeerCertificates[0])+":"+r.TLS.PeerCertificates[0].Subject.CommonName)
	if s.down || r.URL.Path != PathMetrics {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	L := prometheus.L
	c := prometheus.NewCollectors()
	c.Observe("source:rapl", 2*time.Millisecond, nil)
	fams := append([]prometheus.Family{
		{Name: "greenlens_node_energy_joules_total", Help: "Energy of each domain.", Type: prometheus.Counter, Samples: []prometheus.Sample{
			{Labels: L("domain", "package", "source", "rapl", "quality", "measured"), Value: s.pkgJ},
			{Labels: L("domain", "core", "source", "rapl", "quality", "measured"), Value: s.pkgJ / 2},
			{Labels: L("domain", "gpu", "source", "nvml", "quality", "measured"), Value: s.gpu},
		}},
		{Name: "greenlens_node_power_watts", Help: "Power of each domain.", Type: prometheus.Gauge, Samples: []prometheus.Sample{
			{Labels: L("domain", "package", "source", "rapl", "quality", "measured"), Value: s.powerW},
			{Labels: L("domain", "core", "source", "rapl", "quality", "measured"), Value: s.powerW / 2},
			{Labels: L("domain", "wall", "source", "wall_meter", "quality", "platform"), Value: s.powerW + 30},
		}},
		{Name: "greenlens_model_energy_joules_total", Help: "Energy of each model.", Type: prometheus.Counter, Samples: []prometheus.Sample{
			{Labels: L("backend", "ollama", "model", "qwen2.5:0.5b", "method", "share"), Value: 10},
			{Labels: L("backend", "ollama", "model", "llama3", "method", "share"), Value: 5},
		}},
		{Name: "greenlens_build_info", Help: "Version.", Type: prometheus.Gauge, Samples: []prometheus.Sample{{Labels: L("version", "v1.2.3"), Value: 1}}},
	}, c.Families()...)
	w.Header().Set("Content-Type", prometheus.ContentType)
	_ = prometheus.Write(w, fams)
}

// startAgent runs a stub with the certificate cert, which asks its
// clients for a certificate of trust.
func startAgent(t *testing.T, cert pki.Issued, trust *x509.CertPool) *agentStub {
	t.Helper()
	pair, err := tls.LoadX509KeyPair(cert.CertPath, cert.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	s := &agentStub{}
	s.srv = httptest.NewUnstartedServer(s)
	s.srv.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, ClientCAs: trust, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
	s.srv.StartTLS()
	t.Cleanup(s.srv.Close)
	return s
}

func newHub(t *testing.T, a *authority, hubCert pki.Issued, nodes []config.HubNode) (*Hub, *store.Store, string) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	tokens, err := auth.OpenTokens(filepath.Join(t.TempDir(), "tokens"))
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := tokens.Create("dashboard", auth.Viewer, 0)
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Options{
		Config: config.Hub{Listen: config.DefaultHubListen, Interval: config.Duration(15 * time.Second), Nodes: nodes,
			TLS: config.HubTLS{CertFile: hubCert.CertPath, KeyFile: hubCert.KeyPath, CAFile: filepath.Join(a.dir, pki.CACertFile)}},
		Store: st, Tokens: tokens, Version: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, st, secret
}

func byName(f Fleet) map[string]FleetNode {
	out := map[string]FleetNode{}
	for _, n := range f.Nodes {
		out[n.Name] = n
	}
	return out
}

// The hub adds three agents of its fleet and refuses a fourth whose
// certificate its authority did not sign.
func TestHubAggregatesItsFleetAndRefusesAStranger(t *testing.T) {
	a := newAuthority(t)
	stranger := newAuthority(t)
	agents := map[string]*agentStub{
		"node-a": startAgent(t, a.issue("node-a", pki.RoleAgent), a.roots),
		"node-b": startAgent(t, a.issue("node-b", pki.RoleAgent), a.roots),
		"node-c": startAgent(t, a.issue("node-c", pki.RoleAgent), a.roots),
		// This one has a certificate with the right name from another
		// authority, and trusts the hub: only the hub can tell.
		"node-d": startAgent(t, stranger.issue("node-d", pki.RoleAgent), a.roots),
	}
	agents["node-a"].set(1000, 400, 50)
	agents["node-b"].set(2000, 0, 70)
	agents["node-c"].set(500, 100, 20)
	agents["node-d"].set(9000, 9000, 900)
	var nodes []config.HubNode
	for _, name := range []string{"node-a", "node-b", "node-c", "node-d"} {
		nodes = append(nodes, config.HubNode{Name: name, Endpoint: agents[name].srv.URL})
	}
	h, st, _ := newHub(t, a, a.issue("hub", pki.RoleHub), nodes)
	ctx := context.Background()
	h.ScrapeAll(ctx)

	fleet := h.Fleet()
	if fleet.NodesUp != 3 || fleet.PowerW != 140 || len(fleet.Nodes) != 4 {
		t.Fatalf("fleet: %d up, %v W, %d nodes", fleet.NodesUp, fleet.PowerW, len(fleet.Nodes))
	}
	got := byName(fleet)
	na := got["node-a"]
	if !na.Up || na.Error != "" || na.LastSeen == nil || na.Version != "v1.2.3" || na.PowerW != 50 || na.Models != 2 ||
		na.PowerByDomain["wall"] != 80 || na.PowerByDomain["core"] != 25 {
		t.Fatalf("node-a: %+v", na)
	}
	nd := got["node-d"]
	if nd.Up || nd.LastSeen != nil || nd.PowerW != 0 || !strings.Contains(nd.Error, "certificate") {
		t.Fatalf("the stranger: %+v", nd)
	}
	// The stranger never saw a request: the hub ended the handshake.
	agents["node-d"].mu.Lock()
	if len(agents["node-d"].clients) != 0 {
		t.Fatalf("the stranger was asked for its metrics: %v", agents["node-d"].clients)
	}
	agents["node-d"].mu.Unlock()
	agents["node-a"].mu.Lock()
	if len(agents["node-a"].clients) != 1 || agents["node-a"].clients[0] != "hub:hub" {
		t.Fatalf("node-a saw %v", agents["node-a"].clients)
	}
	agents["node-a"].mu.Unlock()

	// The first reading has no interval; the second one measures it.
	if hours, err := st.FleetEnergyBetween(ctx, time.Now().Add(-2*time.Hour), time.Now().Add(time.Hour)); err != nil || len(hours) != 0 {
		t.Fatalf("energy after one reading: %+v %v", hours, err)
	}
	agents["node-a"].set(1300, 450, 55)
	agents["node-b"].set(2000, 0, 5)
	// node-c restarted and counts from zero again.
	agents["node-c"].set(40, 10, 20)
	h.ScrapeAll(ctx)
	hours, err := st.FleetEnergyBetween(ctx, time.Now().Add(-2*time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	energy := map[string]float64{}
	for _, e := range hours {
		energy[e.Node+"/"+e.Domain] += e.EnergyJ
	}
	want := map[string]float64{"node-a/package": 300, "node-a/core": 150, "node-a/gpu": 50, "node-c/package": 40, "node-c/core": 20, "node-c/gpu": 10}
	if len(energy) != len(want) {
		t.Fatalf("energy %v", energy)
	}
	for k, v := range want {
		if energy[k] != v {
			t.Errorf("%s: %v J, want %v", k, energy[k], v)
		}
	}
}

func TestHubServesTheFleet(t *testing.T) {
	a := newAuthority(t)
	agents := map[string]*agentStub{
		"node-a": startAgent(t, a.issue("node-a", pki.RoleAgent), a.roots),
		"node-b": startAgent(t, a.issue("node-b", pki.RoleAgent), a.roots),
	}
	agents["node-a"].set(1000, 400, 50)
	agents["node-b"].set(2000, 0, 70)
	nodes := []config.HubNode{{Name: "node-a", Endpoint: agents["node-a"].srv.URL}, {Name: "node-b", Endpoint: agents["node-b"].srv.URL + "/"}}
	h, _, secret := newHub(t, a, a.issue("hub", pki.RoleHub), nodes)
	ctx := context.Background()
	h.ScrapeAll(ctx)
	agents["node-a"].set(1600, 400, 50)
	h.ScrapeAll(ctx)
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	get := func(path, token string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, body
	}

	if code, _ := get(PathHealth, ""); code != http.StatusOK {
		t.Fatalf("health: %d", code)
	}
	for _, p := range []string{PathFleet, PathEnergy} {
		if code, _ := get(p, ""); code != http.StatusUnauthorized {
			t.Errorf("%s without a token: %d", p, code)
		}
		if code, _ := get(p, "greenlens_000000000000_"+strings.Repeat("a", 43)); code != http.StatusUnauthorized {
			t.Errorf("%s with an unknown token: %d", p, code)
		}
	}
	code, body := get(PathFleet, secret)
	var fleet Fleet
	if code != http.StatusOK || json.Unmarshal(body, &fleet) != nil || fleet.NodesUp != 2 || fleet.PowerW != 120 || fleet.SchemaVersion != 1 {
		t.Fatalf("fleet: %d %s", code, body)
	}
	code, body = get(PathEnergy, secret)
	var energy FleetEnergy
	if code != http.StatusOK || json.Unmarshal(body, &energy) != nil {
		t.Fatalf("energy: %d %s", code, body)
	}
	// Cores are part of the package and are not added twice.
	if energy.Nodes["node-a"] != 600 || energy.Nodes["node-b"] != 0 || energy.EnergyJ != 600 || len(energy.Hours) != 2 {
		t.Fatalf("energy: %+v", energy)
	}
	for _, q := range []string{"?from=yesterday", "?to=soon", "?from=2026-11-15T00:00:00Z&to=2026-11-14T00:00:00Z", "?from=2020-01-01T00:00:00Z&to=2026-01-01T00:00:00Z"} {
		if code, _ := get(PathEnergy+q, secret); code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, code)
		}
	}
	if code, body := get(PathEnergy+"?from=2020-01-01T00:00:00Z&to=2020-01-02T00:00:00Z", secret); code != http.StatusOK || !bytes.Contains(body, []byte(`"hours":[]`)) {
		t.Errorf("an empty period: %d %s", code, body)
	}

	// The metrics carry every series of every node with its name, and
	// are well formed: they parse again with their types.
	code, body = get(PathMetrics, "")
	if code != http.StatusOK {
		t.Fatalf("metrics: %d", code)
	}
	samples, meta, err := promtext.ParseFamilies(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("%v\n%s", err, body)
	}
	if m := meta["greenlens_node_energy_joules_total"]; m.Type != prometheus.Counter || m.Help != "Energy of each domain." {
		t.Fatalf("energy metadata %+v", m)
	}
	if m := meta["greenlens_collector_duration_seconds"]; m.Type != prometheus.Histogram {
		t.Fatalf("histogram metadata %+v", m)
	}
	if strings.Count(string(body), "# TYPE greenlens_node_power_watts ") != 1 {
		t.Fatal("a family is declared more than once")
	}
	find := func(name string, match map[string]string) []promtext.Sample {
		return promtext.Find(samples, name, match)
	}
	if s := find("greenlens_node_energy_joules_total", map[string]string{"node": "node-a", "domain": "package"}); len(s) != 1 || s[0].Value != 1600 || s[0].Labels["source"] != "rapl" {
		t.Fatalf("node-a energy: %+v", s)
	}
	if s := find("greenlens_node_power_watts", map[string]string{"node": "node-b", "domain": "package"}); len(s) != 1 || s[0].Value != 70 {
		t.Fatalf("node-b power: %+v", s)
	}
	if s := find("greenlens_collector_duration_seconds_bucket", map[string]string{"node": "node-a", "le": "+Inf"}); len(s) != 1 || s[0].Value != 1 {
		t.Fatalf("histogram bucket: %+v", s)
	}
	if s := find("greenlens_hub_node_up", nil); len(s) != 2 || s[0].Value != 1 || s[1].Value != 1 {
		t.Fatalf("up: %+v", s)
	}
	if s := find("greenlens_hub_build_info", map[string]string{"version": "test"}); len(s) != 1 {
		t.Fatalf("build info: %+v", s)
	}

	// A node that stops answering is down, and its series go away rather
	// than stay at their last value.
	agents["node-b"].mu.Lock()
	agents["node-b"].down = true
	agents["node-b"].mu.Unlock()
	h.ScrapeAll(ctx)
	_, body = get(PathMetrics, "")
	samples, _, err = promtext.ParseFamilies(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if s := promtext.Find(samples, "greenlens_node_power_watts", map[string]string{"node": "node-b"}); len(s) != 0 {
		t.Fatalf("series of a node that is down: %+v", s)
	}
	if s := promtext.Find(samples, "greenlens_hub_node_up", map[string]string{"node": "node-b"}); len(s) != 1 || s[0].Value != 0 {
		t.Fatalf("up of a node that is down: %+v", s)
	}
	if s := promtext.Find(samples, "greenlens_hub_node_scrape_failures_total", map[string]string{"node": "node-b"}); len(s) != 1 || s[0].Value != 1 {
		t.Fatalf("failures: %+v", s)
	}
	if s := promtext.Find(samples, "greenlens_hub_node_last_seen_timestamp_seconds", map[string]string{"node": "node-b"}); len(s) != 1 || s[0].Value <= 0 {
		t.Fatalf("last seen: %+v", s)
	}
	_, body = get(PathFleet, secret)
	if json.Unmarshal(body, &fleet) != nil || fleet.NodesUp != 1 || fleet.PowerW != 50 || !strings.Contains(byName(fleet)["node-b"].Error, "503") {
		t.Fatalf("fleet with a node down: %s", body)
	}
	// It comes back, and the gap is not counted as energy it used.
	agents["node-b"].mu.Lock()
	agents["node-b"].down = false
	agents["node-b"].mu.Unlock()
	agents["node-b"].set(2300, 0, 70)
	h.ScrapeAll(ctx)
	_, body = get(PathEnergy, secret)
	if json.Unmarshal(body, &energy) != nil || energy.Nodes["node-b"] != 300 {
		t.Fatalf("energy after the node came back: %s", body)
	}
}

func TestHubRefusesImpostors(t *testing.T) {
	a := newAuthority(t)
	hubCert := a.issue("hub", pki.RoleHub)
	// A node of the fleet that answers at the address of another.
	other := startAgent(t, a.issue("node-b", pki.RoleAgent), a.roots)
	other.set(1, 1, 1)
	// A hub certificate used as a server.
	pair, err := tls.LoadX509KeyPair(hubCert.CertPath, hubCert.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	fake := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	fake.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	fake.StartTLS()
	defer fake.Close()
	// Nothing listens at the third address.
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gone := "https://" + l.Addr().String()
	_ = l.Close()

	h, _, _ := newHub(t, a, hubCert, []config.HubNode{
		{Name: "node-a", Endpoint: other.srv.URL}, {Name: "node-x", Endpoint: fake.URL}, {Name: "node-z", Endpoint: gone},
	})
	h.ScrapeAll(context.Background())
	fleet := h.Fleet()
	if fleet.NodesUp != 0 {
		t.Fatalf("%d nodes up", fleet.NodesUp)
	}
	got := byName(fleet)
	if e := got["node-a"].Error; !strings.Contains(e, `of "node-b", not of the agent "node-a"`) {
		t.Errorf("a node at the address of another: %q", e)
	}
	if e := got["node-x"].Error; e == "" {
		t.Error("a hub certificate was accepted as an agent")
	}
	if e := got["node-z"].Error; e == "" {
		t.Error("a missing node has no error")
	}
}

func TestNewErrors(t *testing.T) {
	a := newAuthority(t)
	hubCert := a.issue("hub", pki.RoleHub)
	ca := filepath.Join(a.dir, pki.CACertFile)
	node := []config.HubNode{{Name: "node-a", Endpoint: "https://10.0.0.11:10060"}}
	for name, cfg := range map[string]config.Hub{
		"no nodes":           {TLS: config.HubTLS{CertFile: hubCert.CertPath, KeyFile: hubCert.KeyPath, CAFile: ca}},
		"no authority":       {Nodes: node, TLS: config.HubTLS{CertFile: hubCert.CertPath, KeyFile: hubCert.KeyPath, CAFile: filepath.Join(a.dir, "missing")}},
		"no certificate":     {Nodes: node, TLS: config.HubTLS{CertFile: filepath.Join(a.out, "missing.crt"), KeyFile: hubCert.KeyPath, CAFile: ca}},
		"a node outside":     {Nodes: []config.HubNode{{Name: "node-a", Endpoint: "https://203.0.113.9:10060"}}, TLS: config.HubTLS{CertFile: hubCert.CertPath, KeyFile: hubCert.KeyPath, CAFile: ca}},
		"a node without TLS": {Nodes: []config.HubNode{{Name: "node-a", Endpoint: "http://10.0.0.11:10060"}}, TLS: config.HubTLS{CertFile: hubCert.CertPath, KeyFile: hubCert.KeyPath, CAFile: ca}},
	} {
		if _, err := New(Options{Config: cfg}); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestRunServesUntilCancelled(t *testing.T) {
	a := newAuthority(t)
	agent := startAgent(t, a.issue("node-a", pki.RoleAgent), a.roots)
	agent.set(100, 0, 42)
	hubCert := a.issue("hub", pki.RoleHub)
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready, done := make(chan struct{}), make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Config: config.Hub{Interval: config.Duration(50 * time.Millisecond), Nodes: []config.HubNode{{Name: "node-a", Endpoint: agent.srv.URL}},
				TLS: config.HubTLS{CertFile: hubCert.CertPath, KeyFile: hubCert.KeyPath, CAFile: filepath.Join(a.dir, pki.CACertFile)}},
			Version: "test", Ready: ready, Listener: l, Retention: time.Hour,
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("the hub exited: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the hub did not start")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s%s", l.Addr(), PathMetrics), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !bytes.Contains(body, []byte(`greenlens_node_power_watts{node="node-a",domain="package"`)) {
		t.Fatalf("metrics:\n%s", body)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the hub returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the hub did not stop")
	}
}
