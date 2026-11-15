// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package hub reads the agents of a fleet and shows them together. It
// scrapes the metrics of each agent over mutual TLS, serves them again
// with a node label, keeps the energy of every node hour by hour, and
// answers what the fleet draws now and what it used over a period.
package hub

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/auth"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/exporter/prometheus"
	"github.com/MarioUrenaGarcia/greenlens/internal/intranet"
	"github.com/MarioUrenaGarcia/greenlens/internal/pki"
	"github.com/MarioUrenaGarcia/greenlens/internal/promtext"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

// Paths of the hub.
const (
	PathHealth  = "/healthz"
	PathMetrics = "/metrics"
	PathFleet   = "/api/v1/fleet"
	PathEnergy  = "/api/v1/fleet/energy"
)

// Metrics of the agents the hub reads by name.
const (
	metricEnergy = "greenlens_node_energy_joules_total"
	metricPower  = "greenlens_node_power_watts"
	metricBuild  = "greenlens_build_info"
	metricModels = "greenlens_model_energy_joules_total"
)

const (
	// maxScrape bounds the metrics of one agent.
	maxScrape = 16 << 20
	// scrapeTimeout bounds one reading of one agent.
	scrapeTimeout = 10 * time.Second
	// maxPeriod bounds the period of an energy query.
	maxPeriod = 366 * 24 * time.Hour
	// defaultPeriod is the period of an energy query without one.
	defaultPeriod = 24 * time.Hour
)

// components are the domains whose energy adds up to that of a node; the
// others are part of them or already include them.
var components = []string{"package", "dram", "gpu"}

// Options configures a hub.
type Options struct {
	Config config.Hub
	// Store keeps the energy of the nodes.
	Store *store.Store
	// Tokens are the access tokens of the API of the hub.
	Tokens *auth.Tokens
	Logger *slog.Logger
	// Version is the version of the hub.
	Version string
	// Retention is how long the hours of energy are kept; zero keeps
	// them all.
	Retention time.Duration
	// Ready is closed once the hub listens.
	Ready chan<- struct{}
	// Listener replaces listening on Config.Listen, for tests.
	Listener net.Listener
}

// Hub is a running hub.
type Hub struct {
	o     Options
	log   *slog.Logger
	nodes []*node
	start time.Time
}

// node is one agent and the latest the hub knows of it.
type node struct {
	name     string
	endpoint string
	client   *http.Client

	mu       sync.Mutex
	up       bool
	lastSeen time.Time
	lastErr  string
	failures uint64
	samples  []promtext.Sample
	meta     map[string]promtext.Meta
	// totals are the energy counters of the previous reading, by domain.
	totals map[string]float64
}

// New prepares a hub: it loads the certificate of the hub and the
// authority of the fleet, and builds one client per node that trusts only
// the certificate of that node.
func New(o Options) (*Hub, error) {
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	h := &Hub{o: o, log: o.Logger, start: time.Now()}
	if len(o.Config.Nodes) == 0 {
		return nil, errors.New("hub.nodes lists no agent")
	}
	ca, err := pki.LoadCert(o.Config.TLS.CAFile)
	if err != nil {
		return nil, fmt.Errorf("hub.tls.ca_file: %w", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	pair, err := pki.NewKeyPair(o.Config.TLS.CertFile, o.Config.TLS.KeyFile, func(err error) {
		h.log.Warn("the renewed certificate of the hub cannot be loaded; the previous one stays in use", "error", err)
	})
	if err != nil {
		return nil, fmt.Errorf("hub.tls: %w", err)
	}
	for _, n := range o.Config.Nodes {
		if _, err := intranet.CheckURL(n.Endpoint, "https"); err != nil {
			return nil, fmt.Errorf("node %s: %w", n.Name, err)
		}
		name := n.Name
		tr := intranet.Transport(roots)
		tr.TLSClientConfig.GetClientCertificate = pair.Client
		// The authority vouches for the whole fleet; this makes sure the
		// agent that answers is the node the configuration names, so one
		// node cannot pass for another.
		tr.TLSClientConfig.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("the agent showed no certificate")
			}
			leaf := cs.PeerCertificates[0]
			if pki.Role(leaf) != pki.RoleAgent || leaf.Subject.CommonName != name {
				return fmt.Errorf("the certificate is of %q, not of the agent %q", leaf.Subject.CommonName, name)
			}
			return nil
		}
		h.nodes = append(h.nodes, &node{name: n.Name, endpoint: strings.TrimSuffix(n.Endpoint, "/"),
			client: &http.Client{Transport: tr, Timeout: scrapeTimeout, CheckRedirect: intranet.NoRedirect}})
	}
	return h, nil
}

// Run reads the nodes every interval and serves the API until the context
// ends.
func Run(ctx context.Context, o Options) error {
	h, err := New(o)
	if err != nil {
		return err
	}
	l := o.Listener
	if l == nil {
		var lc net.ListenConfig
		if l, err = lc.Listen(ctx, "tcp", o.Config.Listen); err != nil {
			return err
		}
	}
	srv := &http.Server{
		Handler:           h.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 << 10,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(l) }()
	h.log.Info("hub started", "address", l.Addr().String(), "nodes", len(h.nodes), "interval", o.Config.Interval.D().String())
	h.ScrapeAll(ctx)
	if o.Ready != nil {
		close(o.Ready)
	}
	tick := time.NewTicker(o.Config.Interval.D())
	defer tick.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	h.prune(ctx)
	for {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			err := srv.Shutdown(shutdown)
			cancel()
			for _, n := range h.nodes {
				n.client.CloseIdleConnections()
			}
			h.log.Info("hub stopped")
			return err
		case err := <-serveErr:
			return err
		case <-tick.C:
			h.ScrapeAll(ctx)
		case <-prune.C:
			h.prune(ctx)
		}
	}
}

func (h *Hub) prune(ctx context.Context) {
	if h.o.Retention <= 0 || h.o.Store == nil {
		return
	}
	if _, err := h.o.Store.PruneFleetEnergy(ctx, time.Now().Add(-h.o.Retention)); err != nil {
		h.log.Warn("pruning the energy of the fleet failed", "error", err)
	}
}

// ScrapeAll reads every node once, all at the same time.
func (h *Hub) ScrapeAll(ctx context.Context) {
	var wg sync.WaitGroup
	for _, n := range h.nodes {
		wg.Go(func() { h.scrape(ctx, n) })
	}
	wg.Wait()
}

// scrape reads the metrics of one node and adds the energy it used since
// the previous reading to the hour of now.
func (h *Hub) scrape(ctx context.Context, n *node) {
	now := time.Now()
	samples, meta, err := n.fetch(ctx)
	n.mu.Lock()
	if err != nil {
		wasUp := n.up || n.lastErr == ""
		n.up, n.lastErr, n.samples, n.meta = false, err.Error(), nil, nil
		n.failures++
		n.mu.Unlock()
		if wasUp {
			h.log.Warn("a node cannot be read", "node", n.name, "error", err)
		}
		return
	}
	if !n.up && n.lastErr != "" {
		h.log.Info("a node answers again", "node", n.name)
	}
	totals := map[string]float64{}
	for _, s := range samples {
		if s.Name == metricEnergy {
			totals[s.Labels["domain"]] += s.Value
		}
	}
	used := map[string]float64{}
	if n.totals != nil {
		for domain, total := range totals {
			prev, seen := n.totals[domain]
			switch {
			case !seen:
				// A domain that appears now has no interval to measure.
			case total >= prev:
				used[domain] = total - prev
			default:
				// The agent restarted and counts from zero again.
				used[domain] = total
			}
		}
	}
	n.up, n.lastSeen, n.lastErr, n.samples, n.meta, n.totals = true, now, "", samples, meta, totals
	n.mu.Unlock()
	if h.o.Store != nil && len(used) > 0 {
		if err := h.o.Store.AddFleetEnergy(ctx, n.name, now, used); err != nil {
			h.log.Warn("storing the energy of a node failed", "node", n.name, "error", err)
		}
	}
}

func (n *node) fetch(ctx context.Context) ([]promtext.Sample, map[string]promtext.Meta, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.endpoint+PathMetrics, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("the agent answered %s", resp.Status)
	}
	return promtext.ParseFamilies(io.LimitReader(resp.Body, maxScrape))
}

// families returns the series of every node that answers, each with a
// node label, and the series of the hub itself.
func (h *Hub) families() []prometheus.Family {
	L := prometheus.L
	up := prometheus.Family{Name: "greenlens_hub_node_up", Help: "Whether the latest reading of each node succeeded.", Type: prometheus.Gauge}
	seen := prometheus.Family{Name: "greenlens_hub_node_last_seen_timestamp_seconds", Help: "Time of the latest successful reading of each node.", Type: prometheus.Gauge}
	failed := prometheus.Family{Name: "greenlens_hub_node_scrape_failures_total", Help: "Failed readings of each node since the hub started.", Type: prometheus.Counter}
	build := prometheus.Family{Name: "greenlens_hub_build_info", Help: "Version of the running hub.", Type: prometheus.Gauge,
		Samples: []prometheus.Sample{{Labels: L("version", h.o.Version), Value: 1}}}
	var order []string
	byName := map[string]*prometheus.Family{}
	for _, n := range h.nodes {
		n.mu.Lock()
		v := 0.0
		if n.up {
			v = 1
		}
		up.Samples = append(up.Samples, prometheus.Sample{Labels: L("node", n.name), Value: v})
		if !n.lastSeen.IsZero() {
			seen.Samples = append(seen.Samples, prometheus.Sample{Labels: L("node", n.name), Value: float64(n.lastSeen.UnixMilli()) / 1000})
		}
		failed.Samples = append(failed.Samples, prometheus.Sample{Labels: L("node", n.name), Value: float64(n.failures)})
		for _, s := range n.samples {
			family, suffix := familyOf(s.Name, n.meta)
			f, ok := byName[family]
			if !ok {
				m := n.meta[family]
				f = &prometheus.Family{Name: family, Help: m.Help, Type: m.Type}
				if f.Type == "" {
					f.Type = "untyped"
				}
				byName[family] = f
				order = append(order, family)
			}
			labels := []prometheus.Label{{Name: "node", Value: n.name}}
			for _, k := range sortedKeys(s.Labels) {
				// An agent has no node label; one that came from elsewhere
				// must not replace the name the hub gives.
				if k != "node" {
					labels = append(labels, prometheus.Label{Name: k, Value: s.Labels[k]})
				}
			}
			f.Samples = append(f.Samples, prometheus.Sample{Suffix: suffix, Labels: labels, Value: s.Value})
		}
		n.mu.Unlock()
	}
	out := []prometheus.Family{up, seen, failed, build}
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out
}

// familyOf returns the family of a sample and the suffix of its name: the
// samples of a histogram are named after it with _bucket, _sum or _count.
func familyOf(name string, meta map[string]promtext.Meta) (family, suffix string) {
	if _, ok := meta[name]; ok {
		return name, ""
	}
	for _, s := range []string{"_bucket", "_sum", "_count"} {
		if base, ok := strings.CutSuffix(name, s); ok && meta[base].Type == prometheus.Histogram {
			return base, s
		}
	}
	return name, ""
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
