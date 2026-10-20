// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/calibrate"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

// maxBody limits request bodies; a calibration is a few hundred bytes.
const maxBody = 1 << 20

type peerKey struct{}

// withPeer records the user of the process on the other end of the socket,
// so actions can be audited.
func withPeer(ctx context.Context, c net.Conn) context.Context {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return ctx
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return ctx
	}
	var cred *unix.Ucred
	ctlErr := raw.Control(func(fd uintptr) {
		cred, err = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) //nolint:gosec // descriptors fit in int
	})
	if ctlErr != nil || err != nil || cred == nil {
		return ctx
	}
	return context.WithValue(ctx, peerKey{}, "uid:"+strconv.FormatUint(uint64(cred.Uid), 10))
}

func actor(r *http.Request) string {
	if v, ok := r.Context().Value(peerKey{}).(string); ok {
		return v
	}
	return "unknown"
}

func (a *Agent) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+api.PathHealth, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET "+api.PathReady, func(w http.ResponseWriter, _ *http.Request) {
		if !a.ready.Load() {
			http.Error(w, "no sample yet", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "ready\n")
	})
	mux.HandleFunc("GET "+api.PathStatus, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, a.Status())
	})
	mux.HandleFunc("GET "+api.PathSources, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, a.Status().Channels)
	})
	mux.HandleFunc("GET "+api.PathUsage, a.handleUsage)
	mux.HandleFunc("GET "+api.PathLive, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, a.Live())
	})
	mux.HandleFunc("POST "+api.PathCalibrations, a.handleCalibration)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, api.ErrorBody{Error: msg})
}

// maxRange bounds a usage query to the longest retention there is.
const maxRange = 3 * 366 * 24 * time.Hour

func (a *Agent) handleUsage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, err1 := time.Parse(time.RFC3339, q.Get("from"))
	to, err2 := time.Parse(time.RFC3339, q.Get("to"))
	if err1 != nil || err2 != nil || !from.Before(to) || to.Sub(from) > maxRange {
		writeError(w, http.StatusBadRequest, "from and to must be RFC 3339 times with from before to, at most three years apart")
		return
	}
	var bucket time.Duration
	if b := q.Get("bucket"); b != "" {
		d, err := time.ParseDuration(b)
		if err != nil || d < time.Minute {
			writeError(w, http.StatusBadRequest, "bucket must be a duration of at least 1m")
			return
		}
		if to.Sub(from)/d > 100_000 {
			writeError(w, http.StatusBadRequest, "too many buckets; use a larger bucket")
			return
		}
		bucket = d
	}
	rows, res, err := a.st.NodeEnergy(r.Context(), from, to, bucket, time.Now(), a.retention())
	if err != nil {
		a.log.Error("usage query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "usage query failed")
		return
	}
	writeJSON(w, http.StatusOK, api.Usage{
		SchemaVersion: api.SchemaVersion, From: from, To: to, BucketMS: bucket.Milliseconds(), Resolution: res, Rows: rows,
	})
}

func (a *Agent) handleCalibration(w http.ResponseWriter, r *http.Request) {
	var c calibrate.Result
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		writeError(w, http.StatusBadRequest, "invalid calibration: "+err.Error())
		return
	}
	if err := validCalibration(c); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.st.SaveCalibration(r.Context(), c); err != nil {
		a.log.Error("saving the calibration failed", "error", err)
		writeError(w, http.StatusInternalServerError, "saving the calibration failed")
		return
	}
	who := actor(r)
	if err := a.st.Audit(r.Context(), who, "calibrate", fmt.Sprintf("%d samples over %s", c.Samples, c.Duration)); err != nil {
		a.log.Warn("audit log write failed", "error", err)
	}
	a.mu.Lock()
	a.calib = &c
	a.mu.Unlock()
	a.log.Info("calibration applied", "actor", who, "baseline_cpu_w", c.Baseline().CPUW)
	writeJSON(w, http.StatusCreated, c)
}

func validCalibration(c calibrate.Result) error {
	if c.Samples <= 0 || c.Duration <= 0 || len(c.Domains) == 0 {
		return errors.New("a calibration needs samples, a duration and at least one domain")
	}
	for d, st := range c.Domains {
		if st.P10 < 0 || st.P50 < st.P10 || st.P90 < st.P50 || st.P90 > 100_000 {
			return fmt.Errorf("domain %s: percentiles must be ordered and plausible", d)
		}
	}
	return nil
}

// Status assembles the status document.
func (a *Agent) Status() api.Status {
	a.mu.RLock()
	defer a.mu.RUnlock()
	st := api.Status{
		SchemaVersion: api.SchemaVersion,
		Version:       a.o.Version,
		StartedAt:     a.started,
		IntervalMS:    a.cfg.Agent.SampleInterval.D().Milliseconds(),
		SampleTime:    a.last.Time,
		Samples:       a.samples,
		Estimated:     a.sel.Estimated,
		IdlePolicy:    a.cfg.Attribution.IdlePolicy,
		Self:          api.Self{EnergyJ: a.selfJ, CPUSeconds: float64(a.selfTicks) / 100, RSSBytes: rss()},
	}
	quality := map[source.Domain]source.Quality{}
	for _, c := range a.sel.Channels {
		quality[c.Domain] = c.Quality
	}
	var domains []source.Domain
	for d := range quality {
		domains = append(domains, d)
	}
	slices.Sort(domains)
	for _, d := range domains {
		w, _ := a.last.DomainPower(d)
		st.Domains = append(st.Domains, api.Domain{
			Domain: d, Level: levelName(d.Level()), PowerW: w, EnergyJ: a.energy[d], Quality: quality[d],
		})
	}
	for _, dec := range a.sel.Decisions {
		ch := api.Channel{
			ID: dec.Channel.ID, Source: dec.Channel.Source, Domain: dec.Channel.Domain, Instance: dec.Channel.Instance,
			Quality: dec.Channel.Quality, Method: dec.Channel.Method, Label: dec.Channel.Label,
			Selected: dec.Selected, Reason: dec.Reason.Key,
		}
		for i := 0; i+1 < len(dec.Reason.Args); i += 2 {
			if k, ok := dec.Reason.Args[i].(string); ok {
				if ch.ReasonArgs == nil {
					ch.ReasonArgs = map[string]string{}
				}
				ch.ReasonArgs[k] = fmt.Sprint(dec.Reason.Args[i+1])
			}
		}
		st.Channels = append(st.Channels, ch)
	}
	for _, p := range a.sel.Problems {
		st.Problems = append(st.Problems, api.FromProblem(p))
	}
	for _, p := range a.smp.WrapRisk(a.cfg.Agent.SampleInterval.D()) {
		st.Problems = append(st.Problems, api.FromProblem(p))
	}
	if a.calib != nil {
		c := &api.Calibration{At: a.calib.StartedAt, Baseline: a.calib.Baseline()}
		if p5, ok := a.idle.P5(time.Now()); ok {
			c.IdleNowW = p5
			c.Drift = calibrate.Drift(p5, c.Baseline.CPUW)
		}
		st.Calibration = c
	}
	return st
}

// Live returns the samples kept in memory.
func (a *Agent) Live() api.Live {
	a.mu.RLock()
	samples := a.ring.all()
	a.mu.RUnlock()
	out := api.Live{SchemaVersion: api.SchemaVersion, Points: make([]api.LivePoint, 0, len(samples))}
	for _, s := range samples {
		p := api.LivePoint{Time: s.Time, IntervalMS: s.Interval.Milliseconds(), PowerW: map[source.Domain]float64{}}
		for _, c := range s.Channels {
			p.PowerW[c.Domain] += c.PowerW
		}
		out.Points = append(out.Points, p)
	}
	return out
}

func levelName(l source.Level) string {
	switch l {
	case source.LevelContained:
		return "contained"
	case source.LevelSystem:
		return "system"
	default:
		return "component"
	}
}

// rss returns the resident memory of the agent from /proc/self/statm.
func rss() uint64 {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	var size, resident uint64
	if _, err := fmt.Sscan(string(b), &size, &resident); err != nil {
		return 0
	}
	return resident * uint64(os.Getpagesize()) //nolint:gosec // the page size is positive
}
