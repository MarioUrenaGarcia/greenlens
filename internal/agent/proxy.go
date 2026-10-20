// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/backends"
	"github.com/MarioUrenaGarcia/greenlens/internal/proxy"
)

// startProxy starts the token-counting proxy when it is enabled. The salt
// and the aliases are read before the agent confines itself.
func (a *Agent) startProxy(ctx context.Context) error {
	a.mu.RLock()
	pc := a.cfg.Proxy
	a.mu.RUnlock()
	if !pc.Enabled {
		return nil
	}
	upstream, err := url.Parse(pc.Upstream)
	if err != nil {
		return fmt.Errorf("proxy.upstream: %w", err)
	}
	if pc.ConsumerHeader != "" {
		salt, err := proxy.LoadSalt(filepath.Join(a.dataDir, proxy.SaltFile))
		if err != nil {
			return fmt.Errorf("consumer salt: %w", err)
		}
		aliases := map[string]string{}
		if pc.ConsumerAliasesFile != "" {
			if aliases, err = proxy.LoadAliases(pc.ConsumerAliasesFile); err != nil {
				return fmt.Errorf("consumer aliases: %w", err)
			}
		}
		a.consumers = proxy.NewConsumers(pc.ConsumerHeader, salt, aliases, func(name, keyHMAC string) {
			rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := a.st.EnsureConsumer(rctx, name, keyHMAC, time.Now()); err != nil {
				a.limited("consumer", "recording a consumer failed", "error", err)
			}
		})
	}
	a.meter = proxy.NewMeter()
	a.proxy = proxy.New(proxy.Options{
		Upstream: upstream, Backend: func() string { return a.proxyBackend(upstream) },
		Meter: a.meter, EnergyHeader: pc.PerRequestEnergyHeader,
		ConsumerHeader: pc.ConsumerHeader, Consumers: a.consumers, Log: a.log,
	})
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", pc.Listen)
	if err != nil {
		return fmt.Errorf("proxy: listen on %s: %w", pc.Listen, err)
	}
	a.proxySrv = proxy.NewServer(pc.Listen, a.proxy)
	go func() {
		if err := a.proxySrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.log.Error("the proxy stopped", "error", err)
		}
	}()
	a.log.Info("proxy started", "listen", pc.Listen, "upstream", upstream.Redacted(), "energy_header", pc.PerRequestEnergyHeader, "consumers", pc.ConsumerHeader != "")
	return nil
}

// proxyBackend names the backend the upstream of the proxy belongs to:
// the one whose endpoint has the same port, or a name of its own.
func (a *Agent) proxyBackend(upstream *url.URL) string {
	port := upstream.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[upstream.Scheme]
	}
	a.bmu.Lock()
	defer a.bmu.Unlock()
	for _, b := range a.bs {
		if u, err := url.Parse(b.Endpoint); err == nil && b.Endpoint != "" && (u.Port() == port || (u.Port() == "" && port == "80")) {
			return b.Name
		}
	}
	return "proxy-" + port
}

// withProxy adds the counts of the proxy to the backends that do not count
// tokens themselves. Backends with counters of their own keep them, so no
// token is counted twice.
func (a *Agent) withProxy(readings map[string]backends.Reading) {
	if a.proxy == nil {
		return
	}
	a.mu.RLock()
	upstream, err := url.Parse(a.cfg.Proxy.Upstream)
	a.mu.RUnlock()
	if err != nil {
		return
	}
	name := a.proxyBackend(upstream)
	counters := a.proxy.Counters(name)
	if len(counters) == 0 {
		return
	}
	rd, ok := readings[name]
	switch {
	case !ok:
		rd = backends.Reading{At: time.Now()}
	case rd.Err != nil || rd.Snapshot.TokenCounters:
		return
	}
	rd.Snapshot.Models = counters
	rd.Snapshot.TokenCounters, rd.Snapshot.FromZero = true, true
	readings[name] = rd
}

// proxyStatus describes the proxy for the status document.
func (a *Agent) proxyStatus() *api.Proxy {
	if a.proxy == nil {
		return nil
	}
	a.mu.RLock()
	pc := a.cfg.Proxy
	a.mu.RUnlock()
	upstream, err := url.Parse(pc.Upstream)
	if err != nil {
		return nil
	}
	return &api.Proxy{
		Listen: pc.Listen, Upstream: upstream.Redacted(), Backend: a.proxyBackend(upstream),
		Requests: a.proxy.Requests(), EnergyHeader: pc.PerRequestEnergyHeader, ConsumerHeader: pc.ConsumerHeader,
	}
}
