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

	"github.com/MarioUrenaGarcia/greenlens/internal/backends"
	"github.com/MarioUrenaGarcia/greenlens/internal/bench"
	"github.com/MarioUrenaGarcia/greenlens/internal/calibrate"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/discovery"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/output"
	"github.com/MarioUrenaGarcia/greenlens/internal/procfs"
	"github.com/MarioUrenaGarcia/greenlens/internal/proctrack"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/units"
)

type benchOpts struct {
	kind, endpoint, model, prompts string
	params                         bench.Params
	interval                       time.Duration
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
	f.IntVar(&o.params.Warmup, "warmup", o.params.Warmup, l.T("cli.bench.flag.warmup"))
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
	case o.params.Warmup < 0 || o.params.Warmup > 10:
		return bad("--warmup", o.params.Warmup)
	case o.params.MaxTokens < 1 || o.params.MaxTokens > 32768:
		return bad("--max-tokens", o.params.MaxTokens)
	case o.interval < 50*time.Millisecond || o.interval > 10*time.Second:
		return bad("--interval", o.interval)
	}
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

	// The processes of the backend tell its CPU time apart from the rest
	// of the machine.
	tr := proctrack.New(host)
	if snap, err := procfs.TakeSnapshot(host); err == nil {
		bs := backends.Resolve(host, snap, []config.Backend{{Name: "bench", Kind: o.kind, Endpoint: o.endpoint}}, false)
		tr.Refresh(snap, backends.Roots(bs))
	}
	var ownTicks, busyTicks uint64

	meter := func(fn func() error) (bench.Energy, time.Duration, error) {
		if err := m.smp.Prime(ctx); err != nil && a.verbose {
			_, _ = fmt.Fprintln(a.stderr, err)
		}
		tr.Sample()
		sys0, err := procfs.SystemCPU(a.selfFS())
		if err != nil {
			return bench.Energy{}, 0, err
		}
		var e bench.Energy
		add := func() {
			s, err := m.smp.Sample(ctx)
			if err != nil && a.verbose {
				_, _ = fmt.Fprintln(a.stderr, err)
			}
			for _, c := range s.Channels {
				if c.Domain.Level() == source.LevelComponent {
					e.TotalJ += c.EnergyJ
				}
			}
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
		for _, t := range tr.Sample() {
			ownTicks += t
		}
		if sys1, err := procfs.SystemCPU(a.selfFS()); err == nil {
			busyTicks += sys1.Busy() - sys0.Busy()
		}
		return e, d, runErr
	}

	client := &http.Client{Timeout: 10 * time.Minute}
	target := bench.Target{Kind: kind, Endpoint: o.endpoint, Model: o.model}
	a.say(a.l.T("cli.bench.start", "model", o.model, "endpoint", o.endpoint, "runs", o.params.Runs, "prompts", len(prompts)))
	res, err := bench.Bench(ctx, client, target, o.params, prompts, meter, calibrated, func(run int) {
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
	if !o.noSave {
		if c, err := a.agent(ctx, cfg); err == nil {
			if stored, err := c.SaveBenchmark(ctx, res); err == nil {
				res, saved = stored, true
			} else if !a.quiet {
				_, _ = fmt.Fprintln(a.stderr, err)
			}
		}
	}
	return a.writeBench(cmd, res, saved, o.noSave)
}

func (a *app) writeBench(cmd *cobra.Command, r bench.Result, saved, noSave bool) error {
	l := a.l
	g := output.Grid{Header: []string{l.T("cli.bench.col.run"), l.T("cli.col.energy"), l.T("cli.bench.col.dynamic"), l.T("cli.bench.col.duration"), l.T("cli.col.tokens"), l.T("cli.col.joules_per_token")}}
	for i, run := range r.Runs {
		g.Rows = append(g.Rows, []string{strconv.Itoa(i + 1), units.Energy(run.EnergyJ, a.raw), units.Energy(run.DynamicJ, a.raw),
			fmt.Sprintf("%.1f s", run.DurationS), fmt.Sprintf("%d / %d", run.PromptTokens, run.GeneratedTokens), units.Energy(run.JPerToken, a.raw)})
	}
	if a.out() != output.Table {
		return output.Write(cmd.OutOrStdout(), a.out(), r, g)
	}
	w := cmd.OutOrStdout()
	if err := output.WriteGrid(w, g); err != nil {
		return err
	}
	basis := l.T("cli.bench.basis_dynamic")
	if r.Basis == bench.BasisTotal {
		basis = l.T("cli.bench.basis_total")
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
	case !noSave:
		_, _ = fmt.Fprintln(w, l.T("cli.bench.not_saved"))
	}
	return nil
}
