// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/attribution"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/hostfs"
	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/output"
	"github.com/MarioUrenaGarcia/greenlens/internal/paths"
	"github.com/MarioUrenaGarcia/greenlens/internal/procfs"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
	"github.com/MarioUrenaGarcia/greenlens/internal/units"
)

// MeasureReport is the document of greenlens measure.
type MeasureReport struct {
	SchemaVersion  int                       `json:"schema_version"`
	Target         string                    `json:"target"`
	TargetKind     string                    `json:"target_kind"`
	StartedAt      time.Time                 `json:"started_at"`
	DurationS      float64                   `json:"duration_s"`
	ExitCode       *int                      `json:"exit_code,omitempty"`
	Policy         string                    `json:"idle_policy"`
	Baseline       attribution.Baseline      `json:"baseline"`
	BaselineSource string                    `json:"baseline_source"`
	NodeJ          map[source.Domain]float64 `json:"node_energy_j"`
	CPUJ           float64                   `json:"attributed_cpu_j"`
	DRAMJ          float64                   `json:"attributed_dram_j"`
	PlatformJ      float64                   `json:"attributed_platform_j,omitempty"`
	TotalJ         float64                   `json:"attributed_total_j"`
	CPUShare       float64                   `json:"cpu_share"`
	AveragePowerW  float64                   `json:"average_power_w"`
	Method         string                    `json:"method"`
	Quality        source.Quality            `json:"quality"`
	Indicators     indicators.Values         `json:"indicators"`
	EmissionSource string                    `json:"emission_factor_source"`
	Currency       string                    `json:"currency,omitempty"`
}

// tracker reports the cumulative CPU time of what is measured, in ticks,
// and false once it no longer exists.
type tracker interface {
	ticks() (uint64, bool)
}

type treeTracker struct {
	fsys fs.FS
	root int
}

func (t treeTracker) ticks() (uint64, bool) {
	snap, err := procfs.TakeSnapshot(t.fsys)
	if err != nil {
		return 0, false
	}
	return snap.TreeCPU(t.root)
}

// cgroupTracker reads the CPU time of a cgroup v2 from cpu.stat, in
// microseconds, and converts it to clock ticks of 10 ms.
type cgroupTracker struct {
	fsys fs.FS
	dir  string
}

func (t cgroupTracker) ticks() (uint64, bool) {
	data, err := fs.ReadFile(t.fsys, path.Join(t.dir, "cpu.stat"))
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "usage_usec "); ok {
			usec, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
			return usec / 10_000, err == nil
		}
	}
	return 0, false
}

type measureOpts struct {
	pid      int
	unit     string
	duration time.Duration
	interval time.Duration
	report   string
}

func newMeasureCmd(a *app) *cobra.Command {
	l := a.l
	var o measureOpts
	cmd := &cobra.Command{
		Use:   "measure [-- " + l.T("cli.usage.command_placeholder") + "]",
		Short: l.T("cli.measure.short"),
		Long:  wrap(l.T("cli.measure.long"), helpWidth),
		Example: "  greenlens measure -- ollama run llama3.2 \"Why is the sky blue?\"\n" +
			"  greenlens measure --pid 4242 --duration 5m\n  greenlens measure --unit ollama.service --duration 10m",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dash := cmd.ArgsLenAtDash(); dash > 0 || (dash < 0 && len(args) > 0) {
				return help.New(help.GL0007, "command", cmd.CommandPath(), "args", strings.Join(args, " "))
			}
			targets := 0
			for _, set := range []bool{len(args) > 0, o.pid > 0, o.unit != ""} {
				if set {
					targets++
				}
			}
			if targets != 1 {
				return help.New(help.GL0012)
			}
			return a.measure(cmd, args, o)
		},
	}
	f := cmd.Flags()
	f.IntVar(&o.pid, "pid", 0, l.T("cli.measure.flag.pid"))
	f.StringVar(&o.unit, "unit", "", l.T("cli.measure.flag.unit"))
	f.DurationVar(&o.duration, "duration", 0, l.T("cli.measure.flag.duration"))
	f.DurationVar(&o.interval, "interval", 0, l.T("cli.flag.interval"))
	f.StringVar(&o.report, "report", "", l.T("cli.measure.flag.report"))
	return cmd
}

func (a *app) measure(cmd *cobra.Command, argv []string, o measureOpts) error {
	ctx := cmd.Context()
	cfg, _, err := a.config()
	if err != nil {
		return err
	}
	if o.interval == 0 {
		o.interval = cfg.Agent.SampleInterval.D()
	}
	m, err := a.openMeter(ctx, cfg)
	if err != nil {
		return err
	}
	defer m.close()
	rep := MeasureReport{SchemaVersion: 1, NodeJ: map[source.Domain]float64{}, Policy: cfg.Attribution.IdlePolicy, Method: attribution.Method,
		Quality: source.QualityMeasured}
	if m.sel.Estimated {
		rep.Quality = source.QualityEstimated
	}
	rep.Baseline, rep.BaselineSource = a.baseline(ctx, cfg)
	if rep.BaselineSource == "none" && rep.Policy == string(attribution.Dynamic) {
		a.say(a.l.T("cli.measure.no_baseline"))
	}
	policy, _ := attribution.ParsePolicy(rep.Policy)

	var tr tracker
	var child *exec.Cmd
	done := make(chan error, 1)
	switch {
	case len(argv) > 0:
		rep.TargetKind, rep.Target = "command", strings.Join(argv, " ")
		child = exec.Command(argv[0], argv[1:]...) //nolint:gosec,noctx // the user asks to run this command; it must not be killed on Ctrl-C, which it receives itself
		child.Stdin, child.Stdout, child.Stderr = os.Stdin, a.stdout, a.stderr
	case o.pid > 0:
		rep.TargetKind, rep.Target = "pid", strconv.Itoa(o.pid)
		tr = treeTracker{fsys: a.selfFS(), root: o.pid}
		if _, ok := tr.ticks(); !ok {
			return help.New(help.GL0013, "pid", o.pid)
		}
	default:
		unit := o.unit
		if !strings.Contains(unit, ".") {
			unit += ".service"
		}
		rep.TargetKind, rep.Target = "unit", unit
		dir, ok := findCgroup(a.hostFS(cfg), unit)
		if !ok {
			return help.New(help.GL0014, "unit", unit)
		}
		tr = cgroupTracker{fsys: a.hostFS(cfg), dir: dir}
	}

	if err := m.smp.Prime(ctx); err != nil && !a.quiet {
		_, _ = fmt.Fprintln(a.stderr, err)
	}
	sysFS := a.selfFS()
	sys0, err := procfs.SystemCPU(sysFS)
	if err != nil {
		return err
	}
	rep.StartedAt = time.Now()
	if child != nil {
		if err := child.Start(); err != nil {
			return help.New(help.GL0011, "command", argv[0], "detail", err.Error()).Wrap(err)
		}
		tr = treeTracker{fsys: a.selfFS(), root: child.Process.Pid}
		go func() { done <- child.Wait() }()
	}
	last, _ := tr.ticks()
	var cpuTicks, busyTicks uint64

	// Indicators add up sample by sample, each with the factors of its
	// time; the components and the platform are kept apart until the
	// policy decides which one counts.
	f := cfg.Factors()
	var componentsInd, platformInd indicators.Values
	account := func(targetTicks uint64) {
		s, err := m.smp.Sample(ctx)
		if err != nil && a.verbose {
			_, _ = fmt.Fprintln(a.stderr, err)
		}
		sys1, err := procfs.SystemCPU(sysFS)
		if err != nil {
			return
		}
		dBusy := sys1.Busy() - sys0.Busy()
		sys0 = sys1
		dTarget := uint64(0)
		if targetTicks > last {
			dTarget = targetTicks - last
			last = targetTicks
		}
		cpuTicks += dTarget
		busyTicks += dBusy
		for _, c := range s.Channels {
			rep.NodeJ[c.Domain] += c.EnergyJ
		}
		cpuJ, _ := s.DomainEnergy(source.DomainPackage)
		dramJ, _ := s.DomainEnergy(source.DomainDRAM)
		platJ, hasPlat := s.DomainEnergy(source.DomainPlatform)
		r := attribution.Split(attribution.Node{
			Interval: s.Interval, CPUJ: cpuJ, DRAMJ: dramJ, PlatformJ: platJ, HasPlatform: hasPlat,
			Idle: rep.Baseline, BusyTicks: dBusy,
		}, []attribution.Usage{{ID: rep.Target, Ticks: dTarget}}, policy)
		rep.CPUJ += r[0].CPUJ
		rep.DRAMJ += r[0].DRAMJ
		rep.PlatformJ += r[0].PlatformJ
		componentsInd.Add(f.ComputeOver(r[0].CPUJ+r[0].DRAMJ, s.Time.Add(-s.Interval), s.Time))
		platformInd.Add(f.ComputeOver(r[0].PlatformJ, s.Time.Add(-s.Interval), s.Time))
	}

	tick := time.NewTicker(o.interval)
	defer tick.Stop()
	var deadline <-chan time.Time
	if o.duration > 0 {
		t := time.NewTimer(o.duration)
		defer t.Stop()
		deadline = t.C
	}
	var waitErr error
loop:
	for {
		select {
		case <-tick.C:
			now, alive := tr.ticks()
			if !alive && child == nil {
				account(last)
				break loop
			}
			account(now)
		case waitErr = <-done:
			// The tree is gone; its last CPU time is in the resource usage
			// of the child, which includes every descendant it waited for.
			final := last
			if child.ProcessState != nil {
				if ru, ok := child.ProcessState.SysUsage().(*syscall.Rusage); ok {
					final = max(final, uint64(ru.Utime.Nano()+ru.Stime.Nano())/10_000_000) //nolint:gosec // CPU times are positive
				}
			}
			account(final)
			break loop
		case <-deadline:
			now, _ := tr.ticks()
			account(now)
			if child != nil {
				_ = child.Process.Signal(syscall.SIGTERM)
				waitErr = <-done
			}
			break loop
		case <-ctx.Done():
			// The terminal delivers Ctrl-C to the command too; wait for it
			// to finish so its energy is complete.
			if child != nil {
				waitErr = <-done
			}
			now, _ := tr.ticks()
			account(now)
			break loop
		}
	}
	rep.DurationS = time.Since(rep.StartedAt).Seconds()
	if busyTicks > 0 {
		rep.CPUShare = float64(cpuTicks) / float64(max(busyTicks, cpuTicks))
	}
	rep.TotalJ = rep.CPUJ + rep.DRAMJ
	rep.Indicators = componentsInd
	if policy == attribution.Wall && rep.PlatformJ > 0 {
		rep.TotalJ = rep.PlatformJ
		rep.Indicators = platformInd
	}
	if rep.DurationS > 0 {
		rep.AveragePowerW = rep.TotalJ / rep.DurationS
	}
	rep.EmissionSource = f.EmissionSource
	if f.TariffPerKWh != nil || len(f.Periods) > 0 {
		rep.Currency = f.Currency
	}

	code := 0
	if child != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			code = ee.ExitCode()
		}
		rep.ExitCode = &code
	}
	if err := a.writeMeasure(cmd, rep, o.report, child != nil); err != nil {
		return err
	}
	if code != 0 {
		return &exitError{code: code}
	}
	return nil
}

// baseline finds the idle baseline: from the agent, then from the database
// of this user, else none.
func (a *app) baseline(ctx context.Context, cfg config.Config) (attribution.Baseline, string) {
	actx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if c, err := a.agent(actx, cfg); err == nil {
		if st, err := c.Status(actx); err == nil && st.Calibration != nil {
			return st.Calibration.Baseline, "agent"
		}
	}
	if p, err := paths.Database(cfg, a.getenv); err == nil {
		if _, err := os.Stat(p); err == nil {
			if st, err := store.Open(ctx, p); err == nil {
				defer func() { _ = st.Close() }()
				if r, ok, err := st.LatestCalibration(ctx); err == nil && ok {
					return r.Baseline(), "database"
				}
			}
		}
	}
	return attribution.Baseline{}, "none"
}

// findCgroup looks for the cgroup v2 directory of a systemd unit.
func findCgroup(fsys fs.FS, unit string) (string, bool) {
	const root = "sys/fs/cgroup"
	queue := []string{root}
	for depth := 0; depth < 6 && len(queue) > 0; depth++ {
		var next []string
		for _, dir := range queue {
			entries, err := hostfs.Dirs(fsys, dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				p := path.Join(dir, e)
				if e == unit && hostfs.Exists(fsys, path.Join(p, "cpu.stat")) {
					return p, true
				}
				next = append(next, p)
			}
		}
		queue = next
	}
	return "", false
}

func (a *app) writeMeasure(cmd *cobra.Command, rep MeasureReport, file string, ranCommand bool) error {
	w := cmd.OutOrStdout()
	if ranCommand {
		// The command owns standard output, as with the time utility.
		w = a.stderr
	}
	if file != "" {
		f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // path chosen by the user
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		w = f
	}
	l := a.l
	g := output.Grid{Rows: [][]string{
		{l.T("cli.measure.target"), rep.Target},
		{l.T("cli.measure.duration"), (time.Duration(rep.DurationS * float64(time.Second))).Round(time.Millisecond).String()},
		{l.T("cli.measure.energy"), units.Energy(rep.TotalJ, a.raw)},
		{l.T("cli.measure.cpu"), units.Energy(rep.CPUJ, a.raw)},
		{l.T("cli.measure.dram"), units.Energy(rep.DRAMJ, a.raw)},
		{l.T("cli.measure.power"), units.Power(rep.AveragePowerW, a.raw)},
		{l.T("cli.measure.share"), fmt.Sprintf("%.1f %%", rep.CPUShare*100)},
		{l.T("cli.measure.node"), units.Energy(rep.NodeJ[source.DomainPackage]+rep.NodeJ[source.DomainDRAM]+rep.NodeJ[source.DomainGPU], a.raw)},
		{l.T("cli.measure.co2e"), units.CO2e(rep.Indicators.CO2eG, a.raw) + " (" + rep.EmissionSource + ")"},
	}}
	if rep.Indicators.Cost != nil {
		g.Rows = append(g.Rows, []string{l.T("cli.measure.cost"), units.Money(*rep.Indicators.Cost, rep.Currency, a.raw)})
	}
	if rep.Indicators.WaterL != nil {
		g.Rows = append(g.Rows, []string{l.T("cli.measure.water"), fmt.Sprintf("%.3f L", *rep.Indicators.WaterL)})
	}
	g.Rows = append(g.Rows,
		[]string{l.T("cli.measure.method"), rep.Method + ", " + l.T("cli.measure.policy") + " " + rep.Policy},
		[]string{l.T("cli.col.quality"), a.qualityName(rep.Quality)},
		[]string{l.T("cli.measure.baseline"), units.Power(rep.Baseline.CPUW, a.raw) + " (" + l.T("cli.measure.baseline."+rep.BaselineSource) + ")"},
	)
	if rep.PlatformJ > 0 {
		g.Rows = append(g.Rows, []string{l.T("cli.measure.platform"), units.Energy(rep.PlatformJ, a.raw)})
	}
	if a.out() != output.Table {
		return output.Write(w, a.out(), rep, g)
	}
	if ranCommand {
		_, _ = fmt.Fprintln(w)
	}
	return output.WriteGrid(w, g)
}
