// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package agent runs the measuring agent: it samples the selected energy
// channels, stores the samples, keeps the last 15 minutes in memory,
// accounts its own energy and serves the local API on a Unix socket.
package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/attribution"
	"github.com/MarioUrenaGarcia/greenlens/internal/auth"
	"github.com/MarioUrenaGarcia/greenlens/internal/backends"
	"github.com/MarioUrenaGarcia/greenlens/internal/calibrate"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/drmfdinfo"
	"github.com/MarioUrenaGarcia/greenlens/internal/exporter/prometheus"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/hostfs"
	"github.com/MarioUrenaGarcia/greenlens/internal/paths"
	"github.com/MarioUrenaGarcia/greenlens/internal/procfs"
	"github.com/MarioUrenaGarcia/greenlens/internal/proctrack"
	"github.com/MarioUrenaGarcia/greenlens/internal/proxy"
	"github.com/MarioUrenaGarcia/greenlens/internal/sampler"
	"github.com/MarioUrenaGarcia/greenlens/internal/sandbox"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/estimate"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/registry"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/selector"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
	"github.com/MarioUrenaGarcia/greenlens/internal/usage"
)

// LiveWindow is how much recent data is kept in memory.
const LiveWindow = 15 * time.Minute

// driftAfter is how long the agent samples before its idle estimate is
// compared with the calibration.
const driftAfter = time.Hour

// TokensName is the file of access tokens inside the data directory.
const TokensName = "tokens"

// shutdownGrace bounds the time spent closing, well under the 5 seconds a
// service manager waits after SIGTERM.
const shutdownGrace = 3 * time.Second

// Options configure Run.
type Options struct {
	Config     config.Config
	ConfigPath string
	Getenv     func(string) string
	Logger     *slog.Logger
	// Level, when set, follows logging.level across reloads.
	Level   *slog.LevelVar
	Version string
	// Sources and HostFS replace the hardware, for tests.
	Sources []source.Source
	HostFS  fs.FS
	// NoSandbox skips Landlock. The restriction cannot be undone, so tests
	// that run the agent inside the test process set it.
	NoSandbox bool
	// Ready, when set, is closed once the agent accepts requests.
	Ready chan<- struct{}
	// Reload, when set, replaces SIGHUP as the reload trigger.
	Reload <-chan struct{}
	// UsageInterval replaces usage.Interval, for tests.
	UsageInterval time.Duration
	// RegressionEvery replaces regressionEvery, for tests.
	RegressionEvery time.Duration
}

// Agent is a running agent.
type Agent struct {
	o        Options
	log      *slog.Logger
	hostFS   fs.FS
	selfFS   fs.FS
	srcs     []source.Source
	cands    []selector.Candidate
	st       *store.Store
	dataDir  string
	sockPath string
	started  time.Time
	ready    atomic.Bool

	collectors *prometheus.Collectors

	stream *stream
	// stopping is closed when the agent shuts down, so live streams end
	// instead of holding up the shutdown of the servers.
	stopping chan struct{}

	tokens   *auth.Tokens
	sessions *auth.Sessions
	codes    *auth.Codes
	logins   *limiter
	httpSrv  *http.Server

	// The tracker and the accountant are used only by the sampling loop,
	// the splitter only by the usage worker.
	tracker    *proctrack.Tracker
	drm        *drmfdinfo.Reader
	meter      *proxy.Meter
	proxy      *proxy.Proxy
	proxySrv   *http.Server
	consumers  *proxy.Consumers
	acc        *usage.Accountant
	split      *usage.Splitter
	reader     *backends.Reader
	cuts       chan usage.Cut
	workerDone chan struct{}

	bmu         sync.Mutex
	bs          []backends.Backend
	bstate      map[string]*backendState
	consumerJ   map[string]float64
	lastRegress time.Time

	mu         sync.RWMutex
	pkgQuality source.Quality
	gpuUsers   []source.GPUUser
	cfg        config.Config
	sel        selector.Selection
	smp        *sampler.Sampler
	ring       *ring
	idle       *calibrate.IdleTracker
	calib      *calibrate.Result
	last       sampler.Sample
	samples    uint64
	energy     map[source.Domain]float64
	chanJ      map[string]float64
	chanW      map[string]float64
	// unattributedJ is the energy no backend received.
	unattributedJ float64
	cpuJPerBusyS  float64
	selfJ         float64
	selfTicks     uint64
	selfPrev      uint64
	sysPrev       procfs.CPUTimes
	haveSys       bool
	haveSelf      bool
	lastLog       map[string]time.Time
}

// Run runs the agent until ctx is canceled.
func Run(ctx context.Context, o Options) error {
	a, err := start(ctx, o)
	if err != nil {
		return err
	}
	return a.serve(ctx)
}

func start(ctx context.Context, o Options) (*Agent, error) {
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	a := &Agent{
		o:       o,
		log:     o.Logger,
		cfg:     o.Config,
		hostFS:  o.HostFS,
		selfFS:  hostfs.Root("/"),
		started: time.Now(),
		energy:  map[source.Domain]float64{},
		chanJ:   map[string]float64{},
		chanW:   map[string]float64{},
		// The statistics outlive the sampler, which a reload replaces.
		collectors: prometheus.NewCollectors(),
		stream:     newStream(),
		stopping:   make(chan struct{}),
		consumerJ:  map[string]float64{},
		idle:       calibrate.NewIdleTracker(),
		lastLog:    map[string]time.Time{},
		bstate:     map[string]*backendState{},
		reader:     backends.NewReader(),
		cuts:       make(chan usage.Cut, 4),
		split:      usage.NewSplitter(o.Config.Attribution.MaxModelsPerBackend),
	}
	if a.hostFS == nil {
		a.hostFS = hostfs.Root(o.Config.Agent.HostRoot)
	}
	a.tracker = proctrack.New(a.hostFS)
	a.drm = drmfdinfo.New(a.hostFS)
	policy, _ := attribution.ParsePolicy(o.Config.Attribution.IdlePolicy)
	a.acc = usage.NewAccountant(policy, time.Now())
	a.ring = newRing(int(LiveWindow / o.Config.Agent.SampleInterval.D()))

	var err error
	if a.dataDir, err = paths.DataDir(o.Config, o.Getenv); err != nil {
		return nil, help.New(help.GL4002, "path", "agent.data_dir", "detail", err.Error()).Wrap(err)
	}
	if a.sockPath, err = paths.AgentSocket(o.Config, o.Getenv); err != nil {
		return nil, help.New(help.GL4004).Wrap(err)
	}
	dbPath := filepath.Join(a.dataDir, paths.DatabaseName)
	if a.st, err = store.Open(ctx, dbPath); err != nil {
		return nil, help.New(help.GL4002, "path", dbPath, "detail", err.Error()).Wrap(err)
	}
	tokensPath := o.Config.Server.TokensFile
	if tokensPath == "" {
		tokensPath = filepath.Join(a.dataDir, TokensName)
	}
	if a.tokens, err = auth.OpenTokens(tokensPath); err != nil {
		_ = a.st.Close()
		return nil, help.New(help.GL4013, "path", tokensPath, "detail", err.Error()).Wrap(err)
	}
	a.sessions, a.codes, a.logins = auth.NewSessions(a.st), auth.NewCodes(), newLimiter(loginRate/60.0, loginRate)
	if err := a.setupSources(ctx); err != nil {
		_ = a.st.Close()
		return nil, err
	}
	if c, ok, err := a.st.LatestCalibration(ctx); err != nil {
		a.log.Warn("cannot load the calibration", "error", err)
	} else if ok {
		a.calib = &c
	}
	return a, nil
}

func (a *Agent) setupSources(ctx context.Context) error {
	a.srcs = a.o.Sources
	if a.srcs == nil {
		e := a.cfg.Sources.Estimate
		a.srcs = registry.Sources(registry.Options{
			FS:         a.hostFS,
			HostRoot:   a.cfg.Agent.HostRoot,
			Estimate:   estimate.Override{IdleW: e.IdleWatts, FullW: e.FullWatts, Exponent: e.Exponent},
			NVMLHelper: a.cfg.Sources.NVML.Helper,
		})
	}
	cands, err := registry.Probe(ctx, a.srcs)
	if err != nil {
		a.log.Warn("some energy sources could not be probed", "error", err)
	}
	a.cands = cands
	return a.selectSources(ctx)
}

// selectSources applies the configuration to the probed candidates and
// replaces the sampler when the choice changes.
func (a *Agent) selectSources(ctx context.Context) error {
	sel := selector.Select(a.cands, a.cfg.SelectorOptions())
	if len(sel.Channels) == 0 {
		return help.New(help.GL1001)
	}
	if err := a.st.EnsureChannels(ctx, sel.Channels, time.Now()); err != nil {
		return err
	}
	smp := sampler.New(sel, a.srcs)
	smp.Observe(func(id string, d time.Duration, err error) { a.collectors.Observe("source:"+id, d, err) })
	if err := smp.Prime(ctx); err != nil {
		a.log.Warn("first reading failed", "error", err)
	}
	for _, d := range sel.Decisions {
		a.log.Info("energy channel", "channel", d.Channel.ID, "domain", d.Channel.Domain, "quality", d.Channel.Quality, "selected", d.Selected, "reason", d.Reason.Key)
	}
	for _, p := range sel.Problems {
		a.log.Warn("energy source problem", "code", p.Code, "channel", p.Channel, "detail", help.New(p.Code, p.Args...).Error())
	}
	// The processor quality is the worst of the selected package channels,
	// and the GPU users are the selected sources that report processes.
	var quality source.Quality
	var users []source.GPUUser
	selected := sel.Sources()
	for _, c := range sel.Channels {
		if c.Domain == source.DomainPackage && (quality == "" || c.Quality == source.QualityEstimated) {
			quality = c.Quality
		}
	}
	for _, src := range a.srcs {
		if u, ok := src.(source.GPUUser); ok && slices.Contains(selected, src.ID()) {
			users = append(users, u)
		}
	}
	a.mu.Lock()
	a.sel, a.smp, a.pkgQuality, a.gpuUsers = sel, smp, quality, users
	a.mu.Unlock()
	return nil
}

// listen creates the socket, refusing to take over one that another agent
// still answers on.
func (a *Agent) listen(ctx context.Context) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(a.sockPath), 0o750); err != nil {
		return nil, help.New(help.GL4003, "path", a.sockPath, "detail", err.Error()).Wrap(err)
	}
	d := net.Dialer{Timeout: time.Second}
	if c, err := d.DialContext(ctx, "unix", a.sockPath); err == nil {
		_ = c.Close()
		return nil, help.New(help.GL4005, "path", a.sockPath)
	}
	if err := os.Remove(a.sockPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, help.New(help.GL4003, "path", a.sockPath, "detail", err.Error()).Wrap(err)
	}
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "unix", a.sockPath)
	if err != nil {
		return nil, help.New(help.GL4003, "path", a.sockPath, "detail", err.Error()).Wrap(err)
	}
	// Members of the group of the agent may query it; nobody else.
	if err := os.Chmod(a.sockPath, 0o660); err != nil { //nolint:gosec // group members are the clients of the agent
		_ = l.Close()
		return nil, help.New(help.GL4003, "path", a.sockPath, "detail", err.Error()).Wrap(err)
	}
	return l, nil
}

// confine restricts the agent to the trees it needs once every file and
// device it keeps open has been opened.
func (a *Agent) confine() {
	if a.o.NoSandbox {
		return
	}
	root := a.cfg.Agent.HostRoot
	ro := []string{filepath.Join(root, "sys"), filepath.Join(root, "proc")}
	if f := a.cfg.Proxy.ConsumerAliasesFile; f != "" {
		ro = append(ro, filepath.Dir(f))
	}
	if root != "/" {
		ro = append(ro, "/proc")
	}
	if a.o.ConfigPath != "" {
		ro = append(ro, filepath.Dir(a.o.ConfigPath))
	}
	rw := []string{a.dataDir, filepath.Dir(a.sockPath)}
	if d := filepath.Dir(a.tokens.Path()); d != a.dataDir {
		rw = append(rw, d)
	}
	st, err := sandbox.Restrict(sandbox.Rules{ReadOnly: ro, ReadWrite: rw})
	switch {
	case err != nil:
		a.log.Warn("file access confinement failed", "error", err)
	case st.Enforced:
		a.log.Info("file access confined with Landlock", "abi", st.ABI, "read_only", ro, "read_write", rw)
	default:
		a.log.Warn("this kernel does not provide Landlock; file access is not confined")
	}
}

func (a *Agent) serve(ctx context.Context) error {
	l, err := a.listen(ctx)
	if err != nil {
		_ = a.close()
		return err
	}
	if err := a.startProxy(ctx); err != nil {
		_ = l.Close()
		_ = a.close()
		return err
	}
	hl, err := a.startHTTP(ctx)
	if err != nil {
		_ = l.Close()
		if a.proxySrv != nil {
			_ = a.proxySrv.Close()
		}
		_ = a.close()
		return err
	}
	a.confine()
	// Processes are read before the API answers, so the first status
	// already lists the backends.
	a.refreshBackends(ctx)
	a.workerDone = make(chan struct{})
	workerCtx, stopWorker := context.WithCancel(context.WithoutCancel(ctx))
	defer stopWorker()
	go a.usageWorker(workerCtx)
	srv := &http.Server{
		Handler:           a.handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		ConnContext:       withPeer,
	}
	serveErr := make(chan error, 2)
	go func() { serveErr <- srv.Serve(l) }()
	if hl != nil {
		a.httpSrv = a.httpServer()
		go func() { serveErr <- a.httpSrv.Serve(hl) }()
		a.log.Info("HTTP API listening", "address", hl.Addr().String(), "tls", a.cfg.Server.TLS.CertFile != "")
	}
	if a.o.Ready != nil {
		close(a.o.Ready)
	}
	a.log.Info("agent started", "socket", a.sockPath, "database", a.st.Path(), "interval", a.cfg.Agent.SampleInterval.D().String(),
		"estimated", a.sel.Estimated, "version", a.o.Version)

	reload := a.o.Reload
	if reload == nil {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGHUP)
		defer signal.Stop(sig)
		ch := make(chan struct{})
		go func() {
			for range sig {
				ch <- struct{}{}
			}
		}()
		reload = ch
	}

	err = a.loop(ctx, reload, serveErr)

	close(a.stopping)
	// The parent context is already canceled; closing still needs time.
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer cancel()
	// The last, partial interval is stored too.
	a.cutUsage(time.Now())
	close(a.cuts)
	select {
	case <-a.workerDone:
	case <-shutdown.Done():
		a.log.Warn("the last usage interval was not stored in time")
	}
	if serr := srv.Shutdown(shutdown); serr != nil {
		a.log.Warn("closing the API", "error", serr)
	}
	if a.httpSrv != nil {
		if herr := a.httpSrv.Shutdown(shutdown); herr != nil {
			a.log.Warn("closing the HTTP API", "error", herr)
		}
	}
	if a.proxySrv != nil {
		if perr := a.proxySrv.Shutdown(shutdown); perr != nil {
			a.log.Warn("closing the proxy", "error", perr)
		}
	}
	if rerr := a.st.Rollup(shutdown, time.Now().Add(time.Minute)); rerr != nil {
		a.log.Warn("final rollup", "error", rerr)
	}
	_ = os.Remove(a.sockPath)
	if cerr := a.close(); cerr != nil {
		a.log.Warn("closing", "error", cerr)
	}
	a.log.Info("agent stopped")
	return err
}

func (a *Agent) close() error {
	return errors.Join(registry.Close(a.srcs), a.st.Close())
}

func (a *Agent) loop(ctx context.Context, reload <-chan struct{}, serveErr <-chan error) error {
	interval := a.cfg.Agent.SampleInterval.D()
	tick := time.NewTicker(interval)
	defer tick.Stop()
	minute := time.NewTicker(time.Minute)
	defer minute.Stop()
	every := usage.Interval
	if a.o.UsageInterval > 0 {
		every = a.o.UsageInterval
	}
	usageTick := time.NewTicker(every)
	defer usageTick.Stop()
	a.mu.RLock()
	discoverEvery := a.cfg.Discovery.Interval.D()
	a.mu.RUnlock()
	discover := time.NewTicker(discoverEvery)
	defer discover.Stop()
	hour := time.NewTicker(time.Hour)
	defer hour.Stop()
	lastCheck := time.Now()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-serveErr:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return fmt.Errorf("API server: %w", err)
		case <-tick.C:
			a.step(ctx)
		case now := <-usageTick.C:
			a.cutUsage(now)
		case <-discover.C:
			a.refreshBackends(ctx)
		case <-minute.C:
			if err := a.st.Rollup(ctx, time.Now()); err != nil {
				a.limited("rollup", "rollup failed", "error", err)
			}
			a.checkWrap()
		case <-hour.C:
			a.hourly(ctx, &lastCheck)
		case <-reload:
			if d := a.reload(ctx); d != interval && d > 0 {
				interval = d
				tick.Reset(interval)
			}
			a.mu.RLock()
			every, policy := a.cfg.Discovery.Interval.D(), a.cfg.Attribution.IdlePolicy
			a.mu.RUnlock()
			if p, err := attribution.ParsePolicy(policy); err == nil {
				a.acc.SetPolicy(p)
			}
			if every != discoverEvery && every > 0 {
				discoverEvery = every
				discover.Reset(every)
			}
			a.refreshBackends(ctx)
		}
	}
}

func (a *Agent) hourly(ctx context.Context, lastCheck *time.Time) {
	r := a.retention()
	if n, err := a.st.Prune(ctx, time.Now(), r); err != nil {
		a.log.Warn("pruning old data failed", "error", err)
	} else if n > 0 {
		a.log.Info("old data pruned", "rows", n)
	}
	if err := a.sessions.Prune(ctx); err != nil {
		a.log.Warn("pruning sessions failed", "error", err)
	}
	if err := a.st.Vacuum(ctx); err != nil {
		a.log.Warn("vacuum failed", "error", err)
	}
	if time.Since(*lastCheck) >= 24*time.Hour {
		*lastCheck = time.Now()
		if err := a.st.QuickCheck(ctx); err != nil {
			a.log.Error("database integrity check failed", "error", err)
		}
	}
	a.mu.RLock()
	calib := a.calib
	a.mu.RUnlock()
	if calib != nil {
		if p5, ok := a.idle.P5(time.Now()); ok && time.Since(a.started) >= driftAfter {
			if d := calibrate.Drift(p5, calib.Baseline().CPUW); d > 0.2 {
				a.log.Warn("idle power differs from the calibration", "idle_now_w", p5, "calibrated_w", calib.Baseline().CPUW, "drift", d)
			}
		}
	}
}

func (a *Agent) retention() store.Retention {
	a.mu.RLock()
	defer a.mu.RUnlock()
	r := a.cfg.Retention
	return store.Retention{Raw: r.Raw.D(), Rollup1m: r.Rollup1m.D(), Rollup1h: r.Rollup1h.D()}
}

// step takes one sample and accounts it.
func (a *Agent) step(ctx context.Context) {
	a.mu.RLock()
	smp := a.smp
	a.mu.RUnlock()
	s, err := smp.Sample(ctx)
	if err != nil {
		a.limited("sample", "reading an energy source failed", "error", err)
	}
	if s.Interval <= 0 {
		return
	}
	if err := a.st.InsertSample(ctx, s); err != nil {
		a.limited("insert", "storing a sample failed", "error", err)
	}
	if sys, err := procfs.SystemCPU(a.hostFS); err == nil {
		a.mu.Lock()
		busy, ok := sys.Busy()-a.sysPrev.Busy(), a.haveSys
		a.sysPrev, a.haveSys = sys, true
		a.mu.Unlock()
		if ok {
			a.accountSelf(s, busy)
			a.accountBackends(s, busy)
		}
	}
	a.mu.Lock()
	a.last = s
	a.samples++
	clear(a.chanW)
	for _, c := range s.Channels {
		a.energy[c.Domain] += c.EnergyJ
		a.chanJ[c.Channel] += c.EnergyJ
		a.chanW[c.Channel] = c.PowerW
	}
	a.ring.add(s)
	seq := a.samples
	a.mu.Unlock()
	a.publishSample(seq, s)
	if w, ok := s.DomainPower(source.DomainPackage); ok {
		a.idle.Add(s.Time, w)
	}
	a.ready.Store(true)
}

// accountSelf attributes to the agent the share of energy that matches its
// own CPU time, so its overhead is visible.
func (a *Agent) accountSelf(s sampler.Sample, busy uint64) {
	self, err := procfs.Process(a.selfFS, os.Getpid())
	if err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.haveSelf {
		dSelf := self.CPU() - a.selfPrev
		cpuJ, _ := s.DomainEnergy(source.DomainPackage)
		dramJ, _ := s.DomainEnergy(source.DomainDRAM)
		base := attribution.Baseline{}
		if a.calib != nil {
			base = a.calib.Baseline()
		}
		policy, _ := attribution.ParsePolicy(a.cfg.Attribution.IdlePolicy)
		r := attribution.Split(attribution.Node{Interval: s.Interval, CPUJ: cpuJ, DRAMJ: dramJ, Idle: base, BusyTicks: busy},
			[]attribution.Usage{{ID: "self", Ticks: dSelf}}, policy)
		a.selfJ += r[0].CPUJ + r[0].DRAMJ
		a.selfTicks += dSelf
	}
	a.selfPrev, a.haveSelf = self.CPU(), true
}

func (a *Agent) checkWrap() {
	a.mu.RLock()
	smp, interval := a.smp, a.cfg.Agent.SampleInterval.D()
	a.mu.RUnlock()
	for _, p := range smp.WrapRisk(interval) {
		a.limited("wrap:"+p.Channel, "an energy counter can wrap more than once between samples", "detail", help.New(p.Code, p.Args...).Error())
	}
}

// reload rereads the configuration file. Settings that change where the
// agent keeps its files need a restart; the rest take effect now. It
// returns the sampling interval in effect.
func (a *Agent) reload(ctx context.Context) time.Duration {
	cfg, _, err := config.Load(a.o.ConfigPath, a.o.Getenv)
	a.mu.RLock()
	old := a.cfg
	a.mu.RUnlock()
	if err != nil {
		a.log.Error("configuration not reloaded", "error", err)
		return old.Agent.SampleInterval.D()
	}
	if cfg.Proxy != old.Proxy {
		if a.consumers != nil && cfg.Proxy.ConsumerAliasesFile == old.Proxy.ConsumerAliasesFile && cfg.Proxy.ConsumerAliasesFile != "" {
			// Only the aliases file can change without a restart, and it
			// is read below in any case.
			cfg.Proxy = old.Proxy
		} else {
			a.log.Warn("proxy settings change only after a restart")
			cfg.Proxy = old.Proxy
		}
	}
	if a.consumers != nil && cfg.Proxy.ConsumerAliasesFile != "" {
		if aliases, err := proxy.LoadAliases(cfg.Proxy.ConsumerAliasesFile); err != nil {
			a.log.Error("consumer aliases not reloaded", "error", err)
		} else {
			a.consumers.SetAliases(aliases)
		}
	}
	if cfg.Server != old.Server {
		a.log.Warn("server settings change only after a restart")
		cfg.Server = old.Server
	}
	if cfg.Agent.DataDir != old.Agent.DataDir || cfg.Agent.Socket != old.Agent.Socket || cfg.Agent.HostRoot != old.Agent.HostRoot {
		a.log.Warn("agent.data_dir, agent.socket and agent.host_root change only after a restart")
		cfg.Agent.DataDir, cfg.Agent.Socket, cfg.Agent.HostRoot = old.Agent.DataDir, old.Agent.Socket, old.Agent.HostRoot
	}
	if a.o.Level != nil {
		var lv slog.Level
		if err := lv.UnmarshalText([]byte(cfg.Logging.Level)); err == nil {
			a.o.Level.Set(lv)
		}
	}
	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()
	if !sameSources(old.Sources, cfg.Sources) {
		if err := a.selectSources(ctx); err != nil {
			a.log.Error("new source selection rejected; keeping the previous one", "error", err)
			a.mu.Lock()
			a.cfg.Sources = old.Sources
			a.mu.Unlock()
		}
	}
	a.log.Info("configuration reloaded", "path", a.o.ConfigPath)
	return cfg.Agent.SampleInterval.D()
}

func sameSources(a, b config.Sources) bool {
	key := func(s config.Sources) string {
		var parts []string
		for d, id := range s.Force {
			parts = append(parts, d+"="+id)
		}
		slices.Sort(parts)
		return strconv.FormatBool(s.Auto) + "|" + strings.Join(s.Disable, ",") + "|" + strings.Join(parts, ",")
	}
	return key(a) == key(b)
}

// limited logs a warning at most every five minutes per key, so a sensor
// that keeps failing does not flood the journal. Keys are fixed strings or
// channel IDs, never error texts, so the set of keys stays bounded.
func (a *Agent) limited(key, msg string, args ...any) {
	a.mu.Lock()
	last, seen := a.lastLog[key]
	if seen && time.Since(last) < 5*time.Minute {
		a.mu.Unlock()
		return
	}
	a.lastLog[key] = time.Now()
	a.mu.Unlock()
	a.log.Warn(msg, args...)
}
