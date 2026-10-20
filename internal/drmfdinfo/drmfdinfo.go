// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package drmfdinfo measures how much each process uses a GPU from the
// DRM client statistics the kernel prints in /proc/<pid>/fdinfo, as
// documented in Documentation/gpu/drm-usage-stats.rst: amdgpu and i915
// report the busy time of each engine in nanoseconds, xe the busy cycles of
// each engine class and the total cycles. Only the processes it is given
// are read, and a file descriptor table is listed only when they are
// refreshed.
package drmfdinfo

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

// Client is one DRM client: an open DRM file description.
type Client struct {
	Driver string
	// PDev is the PCI address of the device, as sysfs writes it.
	PDev string
	ID   uint64
	// EngineNS is the busy time of each engine, in nanoseconds.
	EngineNS map[string]uint64
	// Cycles and TotalCycles are the busy and total cycles of each engine.
	Cycles      map[string]uint64
	TotalCycles map[string]uint64
	// Capacity is the number of identical engines behind a name; one when
	// absent.
	Capacity map[string]uint64
}

// Parse reads the fdinfo of one file descriptor. ok is false when it is
// not a DRM client with a device and an ID.
func Parse(data []byte) (Client, bool) {
	c := Client{EngineNS: map[string]uint64{}, Cycles: map[string]uint64{}, TotalCycles: map[string]uint64{}, Capacity: map[string]uint64{}}
	hasID := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok || !strings.HasPrefix(k, "drm-") {
			continue
		}
		v = strings.TrimSpace(v)
		num := func() (uint64, bool) {
			n, err := strconv.ParseUint(strings.TrimSuffix(v, " ns"), 10, 64)
			return n, err == nil
		}
		switch {
		case k == "drm-driver":
			c.Driver = v
		case k == "drm-pdev":
			c.PDev = strings.ToLower(v)
		case k == "drm-client-id":
			c.ID, hasID = num()
		case strings.HasPrefix(k, "drm-engine-capacity-"):
			if n, ok := num(); ok && n > 0 {
				c.Capacity[strings.TrimPrefix(k, "drm-engine-capacity-")] = n
			}
		case strings.HasPrefix(k, "drm-engine-"):
			if n, ok := num(); ok && strings.HasSuffix(v, " ns") {
				c.EngineNS[strings.TrimPrefix(k, "drm-engine-")] = n
			}
		case strings.HasPrefix(k, "drm-total-cycles-"):
			if n, ok := num(); ok {
				c.TotalCycles[strings.TrimPrefix(k, "drm-total-cycles-")] = n
			}
		case strings.HasPrefix(k, "drm-cycles-"):
			if n, ok := num(); ok {
				c.Cycles[strings.TrimPrefix(k, "drm-cycles-")] = n
			}
		}
	}
	return c, c.Driver != "" && c.PDev != "" && hasID
}

type clientKey struct {
	pdev string
	id   uint64
}

type previous struct {
	at     time.Time
	client Client
}

// Reader follows the DRM clients of a set of processes. It is not safe for
// concurrent use.
type Reader struct {
	fsys fs.FS
	now  func() time.Time
	fds  map[int][]string
	prev map[clientKey]previous
}

// New returns a reader of the processes in fsys.
func New(fsys fs.FS) *Reader {
	return &Reader{fsys: fsys, now: time.Now, fds: map[int][]string{}, prev: map[clientKey]previous{}}
}

// Scan lists the DRM file descriptors of pids. Processes whose descriptors
// cannot be read, such as those of another user, are skipped: the kernel
// shows them only to their owner and to privileged readers.
func (r *Reader) Scan(pids []int) {
	fds := map[int][]string{}
	for _, pid := range pids {
		dir := fmt.Sprintf("proc/%d/fdinfo", pid)
		entries, err := fs.ReadDir(r.fsys, dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			p := path.Join(dir, e.Name())
			data, err := fs.ReadFile(r.fsys, p)
			if err != nil {
				continue
			}
			if _, ok := Parse(data); ok {
				fds[pid] = append(fds[pid], p)
			}
		}
	}
	r.fds = fds
}

// Use reads the DRM clients found by the last Scan and returns, for every
// device, the processes holding a client and the share of the device each
// used since the previous call, in percent of one engine summed over
// engines.
func (r *Reader) Use() map[string]source.GPUUse {
	now := r.now()
	out := map[string]source.GPUUse{}
	seen := map[clientKey]bool{}
	pids := make([]int, 0, len(r.fds))
	for pid := range r.fds {
		pids = append(pids, pid)
	}
	// A client shared by several processes, through an inherited
	// descriptor, counts once, for the lowest PID.
	slices.Sort(pids)
	for _, pid := range pids {
		for _, p := range r.fds[pid] {
			data, err := fs.ReadFile(r.fsys, p)
			if err != nil {
				continue
			}
			c, ok := Parse(data)
			if !ok {
				continue
			}
			k := clientKey{c.PDev, c.ID}
			if seen[k] {
				continue
			}
			seen[k] = true
			u := out[c.PDev]
			if !slices.Contains(u.PIDs, pid) {
				u.PIDs = append(u.PIDs, pid)
			}
			prev, ok := r.prev[k]
			if ok {
				if busy := utilization(prev, c, now); busy > 0 {
					if u.Busy == nil {
						u.Busy = map[int]float64{}
					}
					u.Busy[pid] += busy
				}
				c = highest(prev.client, c)
			}
			r.prev[k] = previous{at: now, client: c}
			out[c.PDev] = u
		}
	}
	for k := range r.prev {
		if !seen[k] {
			delete(r.prev, k)
		}
	}
	return out
}

// highest keeps, for every counter, the larger of two readings: a counter
// that went down stays at its previous value until it catches up.
func highest(prev, cur Client) Client {
	for _, m := range []struct{ p, c map[string]uint64 }{{prev.EngineNS, cur.EngineNS}, {prev.Cycles, cur.Cycles}, {prev.TotalCycles, cur.TotalCycles}} {
		for e, v := range m.p {
			m.c[e] = max(m.c[e], v)
		}
	}
	return cur
}

// utilization is the busy share of a client between two readings, in
// percent, summed over its engines. Counters that went down are treated as
// not having moved, as the documentation asks.
func utilization(prev previous, cur Client, now time.Time) float64 {
	capacity := func(e string) float64 {
		if n := cur.Capacity[e]; n > 0 {
			return float64(n)
		}
		return 1
	}
	total := 0.0
	if dt := now.Sub(prev.at).Nanoseconds(); dt > 0 {
		for e, ns := range cur.EngineNS {
			if p := prev.client.EngineNS[e]; ns > p {
				total += float64(ns-p) / float64(dt) / capacity(e)
			}
		}
	}
	for e, cyc := range cur.Cycles {
		p, tp := prev.client.Cycles[e], prev.client.TotalCycles[e]
		tc := cur.TotalCycles[e]
		if cyc > p && tc > tp {
			total += float64(cyc-p) / float64(tc-tp) / capacity(e)
		}
	}
	return 100 * total
}
