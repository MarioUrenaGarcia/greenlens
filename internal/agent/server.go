// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MarioUrenaGarcia/greenlens"
	"github.com/MarioUrenaGarcia/greenlens/internal/about"
	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/auth"
	"github.com/MarioUrenaGarcia/greenlens/internal/bench"
	"github.com/MarioUrenaGarcia/greenlens/internal/calibrate"
	"github.com/MarioUrenaGarcia/greenlens/internal/discovery"
	"github.com/MarioUrenaGarcia/greenlens/internal/i18n"
	"github.com/MarioUrenaGarcia/greenlens/internal/paths"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
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

// actor names who sent a request in the audit log.
func actor(r *http.Request) string {
	if p, ok := auth.FromContext(r.Context()); ok && p.Actor != "" {
		return p.Actor
	}
	return peerActor(r)
}

// peerActor names the local user on the other end of the socket.
func peerActor(r *http.Request) string {
	if v, ok := r.Context().Value(peerKey{}).(string); ok {
		return v
	}
	return "unknown"
}

// route is an endpoint with the least role that may call it. An empty role
// makes it public; socketOnly keeps it off the HTTP listener.
type route struct {
	pattern    string
	role       auth.Role
	socketOnly bool
	h          http.HandlerFunc
}

func (a *Agent) routes() []route {
	return []route{
		{pattern: "GET " + api.PathHealth, h: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ok\n")
		}},
		{pattern: "GET " + api.PathReady, h: func(w http.ResponseWriter, _ *http.Request) {
			if !a.ready.Load() {
				http.Error(w, "no sample yet", http.StatusServiceUnavailable)
				return
			}
			_, _ = io.WriteString(w, "ready\n")
		}},
		{pattern: "GET " + api.PathStatus, role: auth.Viewer, h: func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, a.Status())
		}},
		{pattern: "GET " + api.PathSources, role: auth.Viewer, h: func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, a.Status().Channels)
		}},
		{pattern: "GET " + api.PathUsage, role: auth.Viewer, h: a.handleUsage},
		{pattern: "GET " + api.PathReports, role: auth.Viewer, h: a.handleReport},
		{pattern: "GET " + api.PathRegressions, role: auth.Viewer, h: a.handleRegressions},
		{pattern: "GET " + api.PathBackends, role: auth.Viewer, h: func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, a.Backends())
		}},
		{pattern: "GET " + api.PathLive, role: auth.Viewer, h: func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, a.Live())
		}},
		{pattern: "GET " + api.PathStream, role: auth.Viewer, h: a.handleStream},
		{pattern: "POST " + api.PathCalibrations, role: auth.Operator, h: a.handleCalibration},
		{pattern: "POST " + api.PathCalibRun, role: auth.Operator, h: a.handleStartCalibration},
		{pattern: "GET " + api.PathCalibRun, role: auth.Viewer, h: a.handleCalibrationRun},
		{pattern: "DELETE " + api.PathCalibRun, role: auth.Operator, h: a.handleCancelCalibration},
		{pattern: "GET " + api.PathBenchmarks, role: auth.Viewer, h: a.handleBenchmarks},
		{pattern: "GET " + api.PathBenchmarks + "/{id}", role: auth.Viewer, h: a.handleBenchmark},
		{pattern: "POST " + api.PathBenchmarks, role: auth.Operator, h: a.handleStartBenchmark},
		{pattern: "GET " + api.PathBenchRun, role: auth.Viewer, h: a.handleBenchmarkRun},
		{pattern: "DELETE " + api.PathBenchRun, role: auth.Operator, h: a.handleCancelBenchmark},
		{pattern: "POST " + api.PathBenchResults, role: auth.Operator, h: a.handleSaveBenchmark},
		{pattern: "GET " + api.PathBackup, role: auth.Admin, h: a.handleBackup},
		{pattern: "GET " + api.PathTokens, role: auth.Admin, h: a.handleTokens},
		{pattern: "POST " + api.PathTokens, role: auth.Admin, h: a.handleCreateToken},
		{pattern: "DELETE " + api.PathTokens + "/{id}", role: auth.Admin, h: a.handleRevokeToken},
		{pattern: "GET " + api.PathAudit, role: auth.Admin, h: a.handleAudit},
		{pattern: "GET " + api.PathHelp, role: auth.Viewer, h: a.handleHelp},
		{pattern: "GET " + api.PathHelp + "/{page...}", role: auth.Viewer, h: a.handleHelpPage},
		{pattern: "GET " + api.PathConfig, role: auth.Admin, h: a.handleConfig},
		{pattern: "POST " + api.PathConfigCheck, role: auth.Admin, h: a.handleConfigPreview},
		{pattern: "PUT " + api.PathConfig, role: auth.Admin, h: a.handleConfigApply},
		{pattern: "GET " + api.PathSession, h: a.handleSession},
		{pattern: "POST " + api.PathSessionCodes, role: auth.Admin, socketOnly: true, h: a.handleSessionCode},
		{pattern: "GET " + api.PathCatalogs + "/{lang}", h: handleCatalog},
		{pattern: "GET " + api.PathAbout, role: auth.Viewer, h: func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, about.Get())
		}},
		{pattern: "GET " + api.PathOpenAPI, h: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write(greenlens.OpenAPI)
		}},
	}
}

// handler serves the API on the socket. Its clients are members of the
// group of the agent, trusted like an administrator of the node.
func (a *Agent) handler() http.Handler {
	mux := a.mux(true)
	mux.HandleFunc("GET "+api.PathMetrics, a.handleMetrics)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := auth.Principal{Role: auth.Admin, Actor: peerActor(r), Via: auth.ViaSocket}
		mux.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
	})
}

func (a *Agent) mux(socket bool) *http.ServeMux {
	mux := http.NewServeMux()
	for _, rt := range a.routes() {
		if rt.socketOnly && !socket {
			continue
		}
		mux.Handle(rt.pattern, require(rt.role, rt.h))
	}
	return mux
}

// require lets a request through only when its principal has role.
func require(role auth.Role, h http.HandlerFunc) http.Handler {
	if role == "" {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := auth.FromContext(r.Context())
		switch {
		case !ok:
			writeError(w, http.StatusUnauthorized, "sign in or send an access token")
		case !p.Role.Allows(role):
			writeError(w, http.StatusForbidden, "this needs the "+string(role)+" role")
		default:
			h(w, r)
		}
	})
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
	a.mu.RLock()
	f := a.cfg.Factors()
	a.mu.RUnlock()
	switch by := q.Get("group_by"); by {
	case "":
	case store.ByBackend, store.ByModel, store.ByConsumer:
		query := a.st.Usage
		if f.Varies() {
			query = a.st.UsageHourly
		}
		rows, res, err := query(r.Context(), from, to, by, bucket, time.Now(), a.retention())
		if err != nil {
			a.log.Error("usage query failed", "error", err)
			writeError(w, http.StatusInternalServerError, "usage query failed")
			return
		}
		rows = usageIndicators(rows, f, from, to, bucket, f.Varies())
		writeJSON(w, http.StatusOK, api.BackendUsage{SchemaVersion: api.SchemaVersion, From: from, To: to, GroupBy: by,
			BucketMS: bucket.Milliseconds(), Resolution: res, Rows: rows})
		return
	default:
		writeError(w, http.StatusBadRequest, "group_by must be backend, model or consumer")
		return
	}
	query := a.st.NodeEnergy
	if f.Varies() {
		query = a.st.NodeEnergyHourly
	}
	rows, res, err := query(r.Context(), from, to, bucket, time.Now(), a.retention())
	if err != nil {
		a.log.Error("usage query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "usage query failed")
		return
	}
	rows = nodeIndicators(rows, f, from, to, bucket, f.Varies())
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
	bs := a.Backends().Backends
	px := a.proxyStatus()
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
		Backends:      bs,
		Proxy:         px,
		Self:          api.Self{EnergyJ: a.selfJ, CPUSeconds: float64(a.selfTicks) / 100, RSSBytes: rss()},
		Domains:       []api.Domain{},
		Channels:      []api.Channel{},
	}
	st.CPUJPerBusyS = a.cpuJPerBusyS
	st.Factors = apiFactors(a.cfg.Factors())
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
	for _, b := range bs {
		if b.Problem != nil {
			st.Problems = append(st.Problems, *b.Problem)
		}
	}
	if a.calib != nil {
		c := &api.Calibration{At: a.calib.StartedAt, Baseline: a.calib.Baseline()}
		// The idle estimate needs an hour of samples, as the hourly
		// check of the agent does, before it is compared.
		if p5, ok := a.idle.P5(time.Now()); ok && time.Since(a.started) >= driftAfter {
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
		out.Points = append(out.Points, livePoint(s))
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

func (a *Agent) handleBenchmarks(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 1000")
			return
		}
		limit = n
	}
	list, err := a.st.Benchmarks(r.Context(), limit)
	if err != nil {
		a.log.Error("benchmark query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "benchmark query failed")
		return
	}
	if list == nil {
		list = []bench.Result{}
	}
	writeJSON(w, http.StatusOK, api.Benchmarks{SchemaVersion: api.SchemaVersion, Benchmarks: list})
}

func (a *Agent) handleBenchmark(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "the id of a benchmark is a positive integer")
		return
	}
	b, err := a.st.Benchmark(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no benchmark has this id")
	case err != nil:
		a.log.Error("benchmark query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "benchmark query failed")
	default:
		writeJSON(w, http.StatusOK, b)
	}
}

func (a *Agent) handleSaveBenchmark(w http.ResponseWriter, r *http.Request) {
	var b bench.Result
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		writeError(w, http.StatusBadRequest, "invalid benchmark: "+err.Error())
		return
	}
	if err := validBenchmark(b); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	b.ID = 0
	id, err := a.st.SaveBenchmark(r.Context(), b)
	if err != nil {
		a.log.Error("saving the benchmark failed", "error", err)
		writeError(w, http.StatusInternalServerError, "saving the benchmark failed")
		return
	}
	b.ID = id
	who := actor(r)
	if err := a.st.Audit(r.Context(), who, "benchmark", fmt.Sprintf("%s %s, %d runs", b.Target.Kind, b.Target.Model, len(b.Runs))); err != nil {
		a.log.Warn("audit log write failed", "error", err)
	}
	writeJSON(w, http.StatusCreated, b)
}

var hexDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validBenchmark(b bench.Result) error {
	finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }
	switch {
	case len(b.Runs) < 2 || len(b.Runs) > 1000:
		return errors.New("a benchmark has between 2 and 1000 runs")
	case b.Target.Model == "" || len(b.Target.Model) > 512 || !slices.Contains(discovery.Kinds, b.Target.Kind):
		return errors.New("a benchmark needs a model and a known backend kind")
	case !hexDigest.MatchString(b.PromptSetSHA256):
		return errors.New("prompt_set_sha256 must be 64 hexadecimal digits")
	case !b.EndedAt.After(b.StartedAt):
		return errors.New("a benchmark must end after it starts")
	case !slices.Contains(bench.Bases, b.Basis):
		return errors.New("basis must be backend, dynamic or total")
	}
	for _, r := range b.Runs {
		if !finite(r.EnergyJ) || !finite(r.DynamicJ) || !finite(r.BackendJ) || !finite(r.DurationS) || !finite(r.JPerToken) || r.GeneratedTokens < 0 || r.PromptTokens < 0 {
			return errors.New("run values must be finite and not negative")
		}
	}
	return nil
}

// handleBackup streams a consistent copy of the database. The copy is made
// in the data directory, the only place the agent can write, and removed
// once sent.
func (a *Agent) handleBackup(w http.ResponseWriter, r *http.Request) {
	dir, err := os.MkdirTemp(a.dataDir, "backup-")
	if err != nil {
		a.log.Error("backup failed", "error", err)
		writeError(w, http.StatusInternalServerError, "backup failed")
		return
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			a.log.Warn("backup copy not removed", "path", dir, "error", err)
		}
	}()
	p := filepath.Join(dir, paths.DatabaseName)
	info, err := a.st.Backup(r.Context(), p)
	if err != nil {
		a.log.Error("backup failed", "error", err)
		writeError(w, http.StatusInternalServerError, "backup failed")
		return
	}
	f, err := os.Open(p) //nolint:gosec // the copy was just written by the agent
	if err != nil {
		a.log.Error("backup failed", "error", err)
		writeError(w, http.StatusInternalServerError, "backup failed")
		return
	}
	defer func() { _ = f.Close() }()
	if err := a.st.Audit(r.Context(), actor(r), "backup", fmt.Sprintf("%d bytes, schema %d", info.Bytes, info.SchemaVersion)); err != nil {
		a.log.Warn("audit log write failed", "error", err)
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Bytes, 10))
	w.Header().Set(api.HeaderSchemaVersion, strconv.Itoa(info.SchemaVersion))
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, f); err != nil {
		a.log.Warn("backup not delivered", "error", err)
	}
}

// catalogPrefixes are the parts of the translation catalogs the graphical
// interface uses: its own texts and those of the values and errors it shows.
var catalogPrefixes = []string{"gui.", "errors.", "domain.", "quality.", "level.", "explain.", "selector.reason.", "cli.license.", "cli.bench.cause.", "config.doc."}

// GUICatalog returns the messages of one language that the graphical
// interface uses.
func GUICatalog(lang string) (api.Catalog, error) {
	msgs, err := i18n.Catalog(lang)
	if err != nil {
		return api.Catalog{}, err
	}
	out := api.Catalog{SchemaVersion: api.SchemaVersion, Lang: lang, Languages: i18n.Supported, Messages: map[string]string{}}
	for k, v := range msgs {
		for _, p := range catalogPrefixes {
			if strings.HasPrefix(k, p) {
				out.Messages[k] = v
				break
			}
		}
	}
	return out, nil
}

// handleCatalog serves the messages of one language. It is public, since
// the sign-in page needs it.
func handleCatalog(w http.ResponseWriter, r *http.Request) {
	out, err := GUICatalog(r.PathValue("lang"))
	if err != nil {
		writeError(w, http.StatusNotFound, "unsupported language; available: "+strings.Join(i18n.Supported, ", "))
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	writeJSON(w, http.StatusOK, out)
}
