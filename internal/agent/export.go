// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"context"
	"crypto/x509"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/exporter/otlp"
	"github.com/MarioUrenaGarcia/greenlens/internal/exporter/prometheus"
	"github.com/MarioUrenaGarcia/greenlens/internal/intranet"
)

// collectorOTLP names the OTLP export in the statistics of the collectors.
const collectorOTLP = "otlp"

// exporting holds the OTLP client and what it needs from the files it can
// only read before the agent confines its file access.
type exporting struct {
	mu       sync.Mutex
	client   *otlp.Client
	interval time.Duration
	// changed wakes the export loop when the settings change.
	changed chan struct{}

	roots       *x509.CertPool
	rootsLoaded bool
	token       []byte
	resource    otlp.Resource
}

// setupExport reads the token and the certificate authorities, which the
// confinement hides later, and builds the client.
func (a *Agent) setupExport() {
	e := &exporting{changed: make(chan struct{}, 1)}
	e.resource = otlp.Resource{Version: a.o.Version, Attributes: prometheus.L("service.name", "greenlens", "service.version", a.o.Version)}
	if host, err := os.Hostname(); err == nil && host != "" {
		e.resource.Attributes = append(e.resource.Attributes, prometheus.Label{Name: "host.name", Value: host})
	}
	a.exporting = e
	o := a.cfg.Export.OTLP
	if o.TokenFile != "" {
		token, err := readToken(o.TokenFile)
		if err != nil {
			a.log.Error("OTLP export off: its token cannot be read", "path", o.TokenFile, "error", err)
			return
		}
		e.token = token
	}
	if o.CAFile != "" || (o.Enabled && usesTLS(o.Endpoint)) {
		roots, err := intranet.Roots(o.CAFile)
		if err != nil {
			a.log.Error("OTLP export off: the certificate authorities cannot be read", "path", o.CAFile, "error", err)
			return
		}
		e.roots, e.rootsLoaded = roots, true
	}
	a.applyExport(o)
}

func usesTLS(endpoint string) bool {
	u, err := url.Parse(endpoint)
	return err == nil && u.Scheme == "https"
}

// applyExport replaces the client with one for the given settings.
func (a *Agent) applyExport(o config.OTLP) {
	e := a.exporting
	var client *otlp.Client
	if o.Enabled {
		c, err := otlp.NewClient(o.Endpoint, o.Protocol, e.token, e.roots)
		if err != nil {
			a.log.Error("OTLP export off", "error", err)
		} else {
			client = c
			a.log.Info("OTLP export on", "endpoint", o.Endpoint, "protocol", o.Protocol, "interval", o.Interval.D().String())
		}
	}
	e.mu.Lock()
	old := e.client
	e.client, e.interval = client, o.Interval.D()
	e.mu.Unlock()
	if old != nil {
		old.Close()
	}
	select {
	case e.changed <- struct{}{}:
	default:
	}
}

// exportPaths returns the directories the export reads again on a reload.
func exportPaths(cfg config.Config) []string {
	if f := cfg.Export.OTLP.TokenFile; f != "" {
		return []string{filepath.Dir(f)}
	}
	return nil
}

// reloadExport applies new settings. The files are read only when the
// agent starts, before it confines its file access, so they keep their
// values until a restart.
func (a *Agent) reloadExport(old config.Config, cfg *config.Config) {
	o, n := old.Export.OTLP, &cfg.Export.OTLP
	if o.TokenFile != n.TokenFile || o.CAFile != n.CAFile {
		a.log.Warn("export.otlp.token_file and export.otlp.ca_file change only after a restart")
		n.TokenFile, n.CAFile = o.TokenFile, o.CAFile
	}
	if n.Enabled && usesTLS(n.Endpoint) && !a.exporting.rootsLoaded {
		a.log.Warn("an https export.otlp.endpoint takes effect after a restart, when the certificate authorities are read")
		*n = o
	}
	if *n != o {
		a.applyExport(*n)
	}
}

// exportLoop sends the metrics every interval until the context ends,
// then once more so the collector has the final values.
func (a *Agent) exportLoop(ctx context.Context) {
	e := a.exporting
	for {
		e.mu.Lock()
		interval := e.interval
		e.mu.Unlock()
		if interval <= 0 {
			interval = time.Minute
		}
		t := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			t.Stop()
			final, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			a.exportOnce(final)
			cancel()
			return
		case <-e.changed:
			t.Stop()
		case <-t.C:
			a.exportOnce(ctx)
		}
	}
}

// exportOnce sends the current metrics when the export is on.
func (a *Agent) exportOnce(ctx context.Context) {
	e := a.exporting
	e.mu.Lock()
	client := e.client
	e.mu.Unlock()
	if client == nil {
		return
	}
	now := time.Now()
	err := client.Export(ctx, otlp.Encode(a.metrics(), e.resource, a.started, now))
	a.collectors.Observe(collectorOTLP, time.Since(now), err)
	if err != nil && ctx.Err() == nil {
		a.limited("otlp", "OTLP export failed", "error", err)
	}
}
