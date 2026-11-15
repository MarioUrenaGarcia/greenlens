// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package hub

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/auth"
	"github.com/MarioUrenaGarcia/greenlens/internal/exporter/prometheus"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

// Fleet is the state of every node now.
type Fleet struct {
	SchemaVersion int       `json:"schema_version"`
	Time          time.Time `json:"time"`
	// NodesUp counts the nodes whose latest reading succeeded.
	NodesUp int `json:"nodes_up"`
	// PowerW is the power of the processors, memory and GPUs of the nodes
	// that answer.
	PowerW float64     `json:"power_w"`
	Nodes  []FleetNode `json:"nodes"`
}

// FleetNode is the state of one node.
type FleetNode struct {
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	Up       bool   `json:"up"`
	// LastSeen is the time of the latest successful reading.
	LastSeen *time.Time `json:"last_seen,omitempty"`
	// Error is why the latest reading failed.
	Error   string `json:"error,omitempty"`
	Version string `json:"version,omitempty"`
	// PowerW adds the processors, the memory and the GPUs; PowerByDomain
	// has every domain the node reports, platform and wall included.
	PowerW        float64            `json:"power_w"`
	PowerByDomain map[string]float64 `json:"power_by_domain,omitempty"`
	// Models counts the models with energy attributed since the agent
	// started.
	Models int `json:"models"`
}

// FleetEnergy is the energy of the nodes over a period.
type FleetEnergy struct {
	SchemaVersion int       `json:"schema_version"`
	From          time.Time `json:"from"`
	To            time.Time `json:"to"`
	// Nodes has the energy of the processors, memory and GPUs of each
	// node over the period, in joules.
	Nodes map[string]float64 `json:"nodes"`
	// EnergyJ adds the nodes.
	EnergyJ float64 `json:"energy_j"`
	// Hours has every hour, node and domain of the period.
	Hours []store.FleetEnergy `json:"hours"`
}

// Handler serves the API and the metrics of the hub. The hub listens on
// loopback, where every local user can connect, so everything but the
// health check and the metrics asks for an access token.
func (h *Hub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+PathHealth, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET "+PathMetrics, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", prometheus.ContentType)
		_ = prometheus.Write(w, h.families())
	})
	mux.Handle("GET "+PathFleet, h.viewer(h.handleFleet))
	mux.Handle("GET "+PathEnergy, h.viewer(h.handleEnergy))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		hd.Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

// viewer lets a request through when it carries an access token. Every
// role may read the fleet; the hub changes nothing on the nodes.
func (h *Hub) viewer(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if ok && h.o.Tokens != nil {
			if tk, valid := h.o.Tokens.Verify(strings.TrimSpace(secret)); valid {
				next(w, r.WithContext(auth.WithPrincipal(r.Context(), auth.Principal{Role: tk.Role, Actor: "token:" + tk.Name, Via: auth.ViaToken})))
				return
			}
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="greenlens hub"`)
		writeError(w, http.StatusUnauthorized, "an access token is required; create one with 'greenlens token create'")
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// Fleet returns the state of every node.
func (h *Hub) Fleet() Fleet {
	out := Fleet{SchemaVersion: api.SchemaVersion, Time: time.Now().UTC(), Nodes: []FleetNode{}}
	for _, n := range h.nodes {
		n.mu.Lock()
		fn := FleetNode{Name: n.name, Endpoint: n.endpoint, Up: n.up, Error: n.lastErr}
		if !n.lastSeen.IsZero() {
			t := n.lastSeen.UTC()
			fn.LastSeen = &t
		}
		models := map[string]bool{}
		for _, s := range n.samples {
			switch s.Name {
			case metricPower:
				if fn.PowerByDomain == nil {
					fn.PowerByDomain = map[string]float64{}
				}
				domain := s.Labels["domain"]
				fn.PowerByDomain[domain] += s.Value
				if slices.Contains(components, domain) {
					fn.PowerW += s.Value
				}
			case metricBuild:
				fn.Version = s.Labels["version"]
			case metricModels:
				models[s.Labels["backend"]+"\x00"+s.Labels["model"]] = true
			}
		}
		fn.Models = len(models)
		n.mu.Unlock()
		if fn.Up {
			out.NodesUp++
			out.PowerW += fn.PowerW
		}
		out.Nodes = append(out.Nodes, fn)
	}
	return out
}

func (h *Hub) handleFleet(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.Fleet())
}

// handleEnergy answers the energy of a period, from and to in RFC 3339.
// Without them it is the last 24 hours.
func (h *Hub) handleEnergy(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	to := time.Now().UTC()
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "to must be a time in RFC 3339, such as 2026-11-15T00:00:00Z")
			return
		}
		to = t.UTC()
	}
	from := to.Add(-defaultPeriod)
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "from must be a time in RFC 3339, such as 2026-11-14T00:00:00Z")
			return
		}
		from = t.UTC()
	}
	if !from.Before(to) || to.Sub(from) > maxPeriod {
		writeError(w, http.StatusBadRequest, "from must be before to, at most 366 days apart")
		return
	}
	doc := FleetEnergy{SchemaVersion: api.SchemaVersion, From: from, To: to, Nodes: map[string]float64{}, Hours: []store.FleetEnergy{}}
	if h.o.Store != nil {
		hours, err := h.o.Store.FleetEnergyBetween(r.Context(), from, to)
		if err != nil {
			h.log.Warn("reading the energy of the fleet failed", "error", err)
			writeError(w, http.StatusInternalServerError, "the energy of the fleet cannot be read")
			return
		}
		doc.Hours = hours
	}
	for _, n := range h.nodes {
		doc.Nodes[n.name] = 0
	}
	for _, e := range doc.Hours {
		if slices.Contains(components, e.Domain) {
			doc.Nodes[e.Node] += e.EnergyJ
			doc.EnergyJ += e.EnergyJ
		}
	}
	writeJSON(w, http.StatusOK, doc)
}
