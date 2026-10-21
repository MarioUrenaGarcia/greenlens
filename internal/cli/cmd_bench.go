// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/attribution"
	"github.com/MarioUrenaGarcia/greenlens/internal/backends"
	"github.com/MarioUrenaGarcia/greenlens/internal/bench"
	"github.com/MarioUrenaGarcia/greenlens/internal/calibrate"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/discovery"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/output"
	"github.com/MarioUrenaGarcia/greenlens/internal/procfs"
	"github.com/MarioUrenaGarcia/greenlens/internal/proctrack"
	"github.com/MarioUrenaGarcia/greenlens/internal/sampler"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/units"
	"github.com/MarioUrenaGarcia/greenlens/internal/usage"
)

type benchOpts struct {
	kind, endpoint, model, prompts string
	params                         bench.Params
	interval, warmup, minRun       time.Duration
	noSave                         bool
}

func newBenchCmd(a *app) *cobra.Command {
	l := a.l
	o := benchOpts{params: bench.DefaultParams()}
	cmd := &cobra.Command{
		Use:     "bench",
		Short:   l.T("cli.bench.short"),
		Long:    wrap(l.T("cli.bench.long"), helpWidth),
		Example: "  greenlens bench --model qwen2.5:0.5b\n  greenlens bench --kind llamacpp --endpoint http://127.0.0.1:8080 --model q.gguf --runs 10",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.bench(cmd, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.model, "model", "", l.T("cli.bench.flag.model"))
	f.StringVar(&o.kind, "kind", string(discovery.Ollama), l.T("cli.bench.flag.kind"))
	f.StringVar(&o.endpoint, "endpoint", "", l.T("cli.bench.flag.endpoint"))
	f.StringVar(&o.prompts, "prompts", "", l.T("cli.bench.flag.prompts"))
	f.IntVar(&o.params.Runs, "runs", o.params.Runs, l.T("cli.bench.flag.runs"))
	f.DurationVar(&o.warmup, "warmup", time.Duration(o.params.WarmupS*float64(time.Second)), l.T("cli.bench.flag.warmup"))
	f.DurationVar(&o.minRun, "min-run", time.Duration(o.params.MinRunS*float64(time.Second)), l.T("cli.bench.flag.min_run"))
	f.IntVar(&o.params.MaxTokens, "max-tokens", o.params.MaxTokens, l.T("cli.bench.flag.max_tokens"))
	f.DurationVar(&o.interval, "interval", 100*time.Millisecond, l.T("cli.bench.flag.interval"))
	f.BoolVar(&o.noSave, "no-save", false, l.T("cli.bench.flag.no_save"))
	return cmd
}

func (a *app) bench(cmd *cobra.Command, o benchOpts) error {
	ctx := cmd.Context()
	bad := func(flag string, v any) error {
		return help.New(help.GL0005, "flag", flag, "value", v, "command", cmd.CommandPath())
	}
	kind := discovery.Kind(o.kind)
	switch {
	case !slices.Contains(discovery.Kinds, kind) || kind == discovery.Process:
		return bad("--kind", o.kind)
	case o.model == "":
		return bad("--model", o.model)
	case o.params.Runs < 2 || o.params.Runs > 100:
		return bad("--runs", o.params.Runs)
	case o.warmup < 0 || o.warmup > 10*time.Minute:
		return bad("--warmup", o.warmup)
	case o.minRun < 0 || o.minRun > time.Hour:
		return bad("--min-run", o.minRun)
	case o.params.MaxTokens < 1 || o.params.MaxTokens > 32768:
		return bad("--max-tokens", o.params.MaxTokens)
	case o.interval < 50*time.Millisecond || o.interval > 10*time.Second:
		return bad("--interval", o.interval)
	}
	o.params.WarmupS, o.params.MinRunS = o.warmup.Seconds(), o.minRun.Seconds()
	if o.endpoint == "" {
		o.endpoint = discovery.DefaultEndpoint(kind)
	}
	if err := config.CheckEndpoint(o.endpoint); err != nil {
		return bad("--endpoint", o.endpoint)
	}
	prompts := bench.DefaultPrompts()
	if o.prompts != "" {
		data, err := os.ReadFile(o.prompts)
		if err != nil {
			return bad("--prompts", o.prompts)
		}
		if prompts, err = bench.ParsePrompts(data); err != nil {
			return bad("--prompts", o.prompts)
		}
	}
	cfg, _, err := a.config()
	if err != nil {
		return err
	}
	m, err := a.openMeter(ctx, cfg)
	if err != nil {
		return err
	}
	defer m.close()
	base, baseSource := a.baseline(ctx, cfg)
	calibrated := baseSource != "none"
	if !calibrated {
		a.say(a.l.T("cli.bench.no_baseline"))
	}

	host := a.hostFS(cfg)
	bctx := bench.Snapshot(host)
	idleW := base.CPUW + base.DRAMW
	for _, c := range m.sel.Channels {
		bctx.Channels = append(bctx.Channels, c.ID)
		if c.Domain == source.DomainGPU {
			bctx.GPUs = append(bctx.GPUs, c.Label)
			idleW += base.GPUW[c.Instance]
		}
	}
	throttle0, hasThrottle := bench.Throttles(host)

	// The processes of the backend tell its energy apart from that of the
	// rest of the machine, which would otherwise add its noise to every
	// run.
	const group = "bench"
	tr := proctrack.New(host)
	// Processes are read again before every run: a server can start new
	// ones meanwhile, as Ollama does when it loads a model.
	refresh := func() {
		if snap, err := procfs.TakeSnapshot(host); err == nil {
			bs := backends.Resolve(host, snap, []config.Backend{{Name: group, Kind: o.kind, Endpoint: o.endpoint}}, false)
			tr.Refresh(snap, backends.Roots(bs))
		}
	}
	refresh()
	basis := bench.BasisTotal
	switch {
	case tr.Groups()[group] > 0:
		basis = bench.BasisBackend
	case calibrated:
		basis = bench.BasisDynamic
	}
	if basis != bench.BasisBackend {
		a.say(a.l.T("cli.bench.no_processes"))
	}
	var users []source.GPUUser
	for _, src := range m.srcs {
		if u, ok := src.(source.GPUUser); ok && slices.Contains(m.sel.Sources(), src.ID()) {
			users = append(users, u)
		}
	}
	acc := usage.NewAccountant(attribution.Dynamic, time.Now())
	var ownTicks, busyTicks uint64

	meter := func(fn func() error) (bench.Energy, time.Duration, error) {
		if err := m.smp.Prime(ctx); err != nil && a.verbose {
			_, _ = fmt.Fprintln(a.stderr, err)
		}
		refresh()
		tr.Sample()
		sys0, err := procfs.SystemCPU(a.selfFS())
		if err != nil {
			return bench.Energy{}, 0, err
		}
		prev := sys0
		var e bench.Energy
		chans := m.smp.Channels()
		add := func() {
			s, err := m.smp.Sample(ctx)
			if err != nil && a.verbose {
				_, _ = fmt.Fprintln(a.stderr, err)
			}
			ticks := tr.Sample()
			ownTicks += ticks[group]
			sys, err := procfs.SystemCPU(a.selfFS())
			if err != nil || s.Interval <= 0 {
				return
			}
			busy := sys.Busy() - prev.Busy()
			prev = sys
			if e.DomainsJ == nil {
				e.DomainsJ = map[string]float64{}
			}
			for _, c := range s.Channels {
				e.DomainsJ[string(c.Domain)] += c.EnergyJ
				if c.Domain.Level() == source.LevelComponent {
					e.TotalJ += c.EnergyJ
				}
			}
			e.BackendJ += acc.Add(backendSample(s, chans, ticks, busy, base, users, tr))[group]
		}
		start := time.Now()
		done := make(chan error, 1)
		go func() { done <- fn() }()
		tick := time.NewTicker(o.interval)
		defer tick.Stop()
		var runErr error
	loop:
		for {
			select {
			case <-tick.C:
				add()
			case runErr = <-done:
				add()
				break loop
			}
		}
		d := time.Since(start)
		e.IdleJ = idleW * d.Seconds()
		busyTicks += prev.Busy() - sys0.Busy()
		acc.Cut(time.Now())
		return e, d, runErr
	}

	client := &http.Client{Timeout: 10 * time.Minute}
	target := bench.Target{Kind: kind, Endpoint: o.endpoint, Model: o.model}
	a.say(a.l.T("cli.bench.start", "model", o.model, "endpoint", o.endpoint, "runs", o.params.Runs, "prompts", len(prompts), "warmup", o.warmup, "min", o.minRun))
	res, err := bench.Bench(ctx, client, target, o.params, prompts, meter, basis, calibrated, func(run int) {
		a.say(a.l.T("cli.bench.progress", "run", run, "runs", o.params.Runs))
	})
	if err != nil {
		var re *bench.RequestError
		if errors.As(err, &re) {
			return help.New(help.GL2003, "endpoint", o.endpoint, "detail", re.Error(), "model", o.model).Wrap(err)
		}
		if errors.Is(err, context.Canceled) {
			return err
		}
		return help.New(help.GL2001, "name", o.kind, "endpoint", o.endpoint, "detail", err.Error()).Wrap(err)
	}
	bctx.EndTempC = calibrate.Temperature(host)
	if throttle1, ok := bench.Throttles(host); ok && hasThrottle {
		t := throttle1 > throttle0
		bctx.Throttled = &t
	}
	if busyTicks > 0 {
		bctx.OtherCPUShare = max(0, 1-float64(ownTicks)/float64(busyTicks))
	}
	res.Context = bctx
	res.Causes = bench.Diagnose(res)
	if m.sel.Estimated && !a.quiet {
		a.say(a.l.T("cli.bench.estimated"))
	}

	saved := false
	var saveErr error
	if !o.noSave {
		if c, err := a.agent(ctx, cfg); err == nil {
			if stored, err := c.SaveBenchmark(ctx, res); err == nil {
				res, saved = stored, true
			} else {
				saveErr = err
			}
		}
	}
	return a.writeBench(cmd, res, saved, o.noSave, saveErr)
}

// backendSample builds the accounting of one sample, as the agent does:
// the CPU time of the backend and, for every GPU, the use of each process.
func backendSample(s sampler.Sample, chans map[string]source.Channel, ticks map[string]uint64, busy uint64, base attribution.Baseline, users []source.GPUUser, tr *proctrack.Tracker) usage.Sample {
	cpuJ, _ := s.DomainEnergy(source.DomainPackage)
	dramJ, _ := s.DomainEnergy(source.DomainDRAM)
	out := usage.Sample{Node: attribution.Node{Interval: s.Interval, CPUJ: cpuJ, DRAMJ: dramJ, Idle: base, BusyTicks: busy}, Ticks: ticks}
	use := map[string]source.GPUUse{}
	for _, u := range users {
		for inst, g := range u.GPUUse() {
			use[inst] = g
		}
	}
	for _, c := range s.Channels {
		if c.Domain != source.DomainGPU {
			continue
		}
		ch := chans[c.Channel]
		g := usage.GPU{GPU: attribution.GPU{Interval: s.Interval, EnergyJ: c.EnergyJ, IdleW: base.GPUW[ch.Instance], Busy: map[string]float64{}}, Quality: ch.Quality}
		u := use[ch.Instance]
		for pid, b := range u.Busy {
			if grp, ok := tr.GroupOf(pid); ok {
				g.Busy[grp] += b
			} else {
				g.Other += b
			}
		}
		for _, pid := range u.PIDs {
			if grp, ok := tr.GroupOf(pid); ok {
				g.Holders = append(g.Holders, grp)
			}
		}
		out.GPUs = append(out.GPUs, g)
	}
	return out
}

func (a *app) writeBench(cmd *cobra.Command, r bench.Result, saved, noSave bool, saveErr error) error {
	l := a.l
	g := output.Grid{Header: []string{l.T("cli.bench.col.run"), l.T("cli.col.energy"), l.T("cli.bench.col.backend"), l.T("cli.bench.col.duration"), l.T("cli.col.tokens"), l.T("cli.col.joules_per_token")}}
	for i, run := range r.Runs {
		g.Rows = append(g.Rows, []string{strconv.Itoa(i + 1), units.Energy(run.EnergyJ, a.raw), units.Energy(run.BackendJ, a.raw),
			fmt.Sprintf("%.1f s", run.DurationS), fmt.Sprintf("%d / %d", run.PromptTokens, run.GeneratedTokens), units.Energy(run.JPerToken, a.raw)})
	}
	if a.out() != output.Table {
		return output.Write(cmd.OutOrStdout(), a.out(), r, g)
	}
	w := cmd.OutOrStdout()
	if err := output.WriteGrid(w, g); err != nil {
		return err
	}
	basis := l.T("cli.bench.basis_" + r.Basis)
	if r.Basis == bench.BasisBackend && !r.Calibrated {
		basis = l.T("cli.bench.basis_backend_idle")
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, l.T("cli.bench.summary", "mean", units.Energy(r.JPerToken.Mean, a.raw), "ci", units.Energy(r.JPerToken.CI95, a.raw),
		"cv", strconv.FormatFloat(100*r.JPerToken.CV, 'f', 1, 64), "basis", basis))
	if len(r.Causes) > 0 || r.JPerToken.CV > bench.UnstableCV {
		_, _ = fmt.Fprintln(w, a.paint(l.T("cli.bench.unstable", "cv", strconv.FormatFloat(100*r.JPerToken.CV, 'f', 1, 64)), true))
		for _, c := range r.Causes {
			_, _ = fmt.Fprintln(w, "  - "+l.T("cli.bench.cause."+c))
		}
	}
	switch {
	case saved:
		_, _ = fmt.Fprintln(w, l.T("cli.bench.saved", "id", r.ID))
	case saveErr != nil:
		_, _ = fmt.Fprintln(w, l.T("cli.bench.save_failed", "detail", saveErr.Error()))
	case !noSave:
		_, _ = fmt.Fprintln(w, l.T("cli.bench.not_saved"))
	}
	return nil
}
