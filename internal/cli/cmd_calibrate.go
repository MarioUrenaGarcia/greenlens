// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/calibrate"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/output"
	"github.com/MarioUrenaGarcia/greenlens/internal/paths"
	"github.com/MarioUrenaGarcia/greenlens/internal/sampler"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/registry"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/selector"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
	"github.com/MarioUrenaGarcia/greenlens/internal/units"
)

// CalibrationReport is the document of greenlens calibrate.
type CalibrationReport struct {
	SchemaVersion int              `json:"schema_version"`
	Result        calibrate.Result `json:"result"`
	SavedTo       string           `json:"saved_to"`
}

// meter is a sampler over the selected channels and what it needs to close.
type meter struct {
	sel  selector.Selection
	smp  *sampler.Sampler
	srcs []source.Source
}

func (m *meter) close() { _ = registry.Close(m.srcs) }

// openMeter probes and selects sources the way the agent does.
func (a *app) openMeter(ctx context.Context, cfg config.Config) (*meter, error) {
	srcs := a.sources(cfg)
	cands, err := registry.Probe(ctx, srcs)
	if err != nil && !a.quiet {
		_, _ = fmt.Fprintln(a.stderr, err)
	}
	sel := selector.Select(cands, cfg.SelectorOptions())
	if len(sel.Channels) == 0 {
		_ = registry.Close(srcs)
		return nil, help.New(help.GL1001)
	}
	return &meter{sel: sel, smp: sampler.New(sel, srcs), srcs: srcs}, nil
}

func newCalibrateCmd(a *app) *cobra.Command {
	l := a.l
	var duration, interval time.Duration
	cmd := &cobra.Command{
		Use:     "calibrate",
		Short:   l.T("cli.calibrate.short"),
		Long:    wrap(l.T("cli.calibrate.long"), helpWidth),
		Example: "  greenlens calibrate\n  greenlens calibrate --duration 5m",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := a.config()
			if err != nil {
				return err
			}
			if duration < 10*time.Second {
				return help.New(help.GL0005, "flag", "--duration", "value", duration, "command", cmd.CommandPath())
			}
			if interval == 0 {
				interval = cfg.Agent.SampleInterval.D()
			}
			m, err := a.openMeter(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer m.close()
			if m.sel.Estimated {
				// The idle power of a model is a parameter, not a measurement.
				return help.New(help.GL1001)
			}
			a.say(l.T("cli.calibrate.start", "duration", duration))
			tty := isTerminal(a.stderr)
			res, err := calibrate.Run(cmd.Context(), m.smp, a.hostFS(cfg), duration, interval, func(el time.Duration) {
				if tty && !a.quiet {
					_, _ = fmt.Fprintf(a.stderr, "\r  %s / %s", el.Round(time.Second), duration)
				}
			})
			if tty && !a.quiet {
				_, _ = fmt.Fprintln(a.stderr)
			}
			if err != nil {
				return err
			}
			rep := CalibrationReport{SchemaVersion: 1, Result: res}
			rep.SavedTo, err = a.saveCalibration(cmd.Context(), cfg, res)
			if werr := a.writeCalibration(cmd, rep); werr != nil {
				return werr
			}
			return err
		},
	}
	cmd.Flags().DurationVar(&duration, "duration", calibrate.DefaultDuration, l.T("cli.calibrate.flag.duration"))
	cmd.Flags().DurationVar(&interval, "interval", 0, l.T("cli.flag.interval"))
	return cmd
}

// saveCalibration hands the result to the agent, which applies it at once;
// without an agent it is stored in the database of this user.
func (a *app) saveCalibration(ctx context.Context, cfg config.Config, r calibrate.Result) (string, error) {
	actx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if c, err := a.agent(actx, cfg); err == nil {
		if err := c.SaveCalibration(ctx, r); err != nil {
			return "", err
		}
		return c.Socket, nil
	}
	p, err := paths.Database(cfg, a.getenv)
	if err != nil {
		return "", help.New(help.GL4002, "path", "agent.data_dir", "detail", err.Error()).Wrap(err)
	}
	st, err := store.Open(ctx, p)
	if err != nil {
		return "", help.New(help.GL4002, "path", p, "detail", err.Error()).Wrap(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.SaveCalibration(ctx, r); err != nil {
		return "", help.New(help.GL4002, "path", p, "detail", err.Error()).Wrap(err)
	}
	return p, nil
}

func (a *app) writeCalibration(cmd *cobra.Command, rep CalibrationReport) error {
	l := a.l
	r := rep.Result
	g := output.Grid{Header: []string{l.T("cli.col.domain"), "P10", "P50", "P90", l.T("cli.col.quality")}}
	var domains []source.Domain
	for d := range r.Domains {
		domains = append(domains, d)
	}
	slices.Sort(domains)
	for _, d := range domains {
		st := r.Domains[d]
		g.Rows = append(g.Rows, []string{a.domainName(d), units.Power(st.P10, a.raw), units.Power(st.P50, a.raw), units.Power(st.P90, a.raw),
			a.qualityName(source.Quality(r.Quality[d]))})
	}
	if a.out() != output.Table {
		return output.Write(cmd.OutOrStdout(), a.out(), rep, g)
	}
	w := cmd.OutOrStdout()
	if err := output.WriteGrid(w, g); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "\n%s %d, %s %.0f MHz, %s %s, %s %.1f C\n", l.T("cli.calibrate.samples"), r.Samples,
		l.T("cli.calibrate.frequency"), r.FrequencyMHz, l.T("cli.calibrate.governor"), r.Governor,
		l.T("cli.calibrate.temperature"), r.TemperatureC)
	if rep.SavedTo != "" {
		_, _ = fmt.Fprintln(w, l.T("cli.calibrate.saved", "where", rep.SavedTo))
	}
	return nil
}

func isTerminal(w any) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
