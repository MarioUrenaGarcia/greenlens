// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package backends keeps the inference backends of the node: the ones
// declared in the configuration and the ones discovered, with the
// processes that run each and the counters read from its endpoint.
package backends

import (
	"context"
	"io/fs"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/connector"
	"github.com/MarioUrenaGarcia/greenlens/internal/discovery"
	"github.com/MarioUrenaGarcia/greenlens/internal/procfs"
)

// Backend is one inference server and the processes that run it.
type Backend struct {
	Name        string         `json:"name"`
	Kind        discovery.Kind `json:"kind"`
	Endpoint    string         `json:"endpoint,omitempty"`
	Static      bool           `json:"static"`
	ContainerID string         `json:"container_id,omitempty"`
	// Roots are the root processes of the backend; their descendants
	// belong to it too.
	Roots []int `json:"pids,omitempty"`
}

// port returns the port of an endpoint, or 0.
func port(endpoint string) int {
	u, err := url.Parse(endpoint)
	if err != nil {
		return 0
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		switch u.Scheme {
		case "http":
			return 80
		case "https":
			return 443
		}
		return 0
	}
	return p
}

// Resolve combines the backends of the configuration with the processes in
// snap. A declared backend owns the engine process listening on the port of
// its endpoint, or the processes of its executable. With discover, every
// other engine process becomes a backend named after its kind and port.
func Resolve(fsys fs.FS, snap procfs.Snapshot, static []config.Backend, discover bool) []Backend {
	cands := discovery.ScanSnapshot(fsys, snap)
	owned := map[int]bool{}
	var out []Backend
	for _, s := range static {
		b := Backend{Name: s.Name, Kind: discovery.Kind(s.Kind), Endpoint: s.Endpoint, Static: true}
		if s.Process != "" {
			b.Roots = discovery.Executable(fsys, snap, s.Process)
		} else if p := port(s.Endpoint); p > 0 {
			for _, c := range cands {
				if !owned[c.PID] && slices.ContainsFunc(c.Endpoints, func(e string) bool { return port(e) == p }) {
					b.Roots = append(b.Roots, c.PID)
				}
			}
		}
		for _, pid := range b.Roots {
			owned[pid] = true
			if b.ContainerID == "" {
				b.ContainerID = discovery.Container(fsys, pid)
			}
		}
		out = append(out, b)
	}
	if !discover {
		return out
	}
	names := map[string]bool{}
	for _, b := range out {
		names[b.Name] = true
	}
	for _, c := range cands {
		if owned[c.PID] {
			continue
		}
		endpoint := ""
		if len(c.Endpoints) > 0 {
			endpoint = c.Endpoints[0]
		}
		name := discovery.Name(c.Kind, endpoint)
		// Two servers of one kind without a visible port, or a discovered
		// server that takes the name of a declared one, are told apart by
		// process.
		if names[name] {
			name += "-pid" + strconv.Itoa(c.PID)
		}
		names[name] = true
		out = append(out, Backend{Name: name, Kind: c.Kind, Endpoint: endpoint, ContainerID: c.ContainerID, Roots: []int{c.PID}})
	}
	return out
}

// Roots maps every backend to its root processes, as the tracker expects.
func Roots(bs []Backend) map[string][]int {
	out := make(map[string][]int, len(bs))
	for _, b := range bs {
		out[b.Name] = b.Roots
	}
	return out
}

// Reading is the result of reading the counters of one backend.
type Reading struct {
	At       time.Time
	Snapshot connector.Snapshot
	Err      error
}

// Reader reads the counters of backends.
type Reader struct {
	client *http.Client
	scrape func(ctx context.Context, c *http.Client, kind discovery.Kind, endpoint string) (connector.Snapshot, error)
	now    func() time.Time
}

// NewReader returns a reader with the connector client.
func NewReader() *Reader {
	return &Reader{client: connector.Client(), scrape: connector.Scrape, now: time.Now}
}

// Read reads every backend that has an endpoint, all at once, each bounded
// by the timeout of the connector.
func (r *Reader) Read(ctx context.Context, bs []Backend) map[string]Reading {
	out := map[string]Reading{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, b := range bs {
		if b.Endpoint == "" || b.Kind == discovery.Process {
			continue
		}
		wg.Go(func() {
			snap, err := r.scrape(ctx, r.client, b.Kind, b.Endpoint)
			mu.Lock()
			out[b.Name] = Reading{At: r.now(), Snapshot: snap, Err: err}
			mu.Unlock()
		})
	}
	wg.Wait()
	return out
}
