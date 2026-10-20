// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/output"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/units"
)

func newStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Short:   a.l.T("cli.status.short"),
		Long:    wrap(a.l.T("cli.status.long"), helpWidth),
		Example: "  greenlens status\n  greenlens status --output json",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := a.config()
			if err != nil {
				return err
			}
			c, err := a.agent(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			st, err := c.Status(cmd.Context())
			if err != nil {
				return err
			}
			return a.writeStatus(cmd, st)
		},
	}
}

func (a *app) domainName(d source.Domain) string { return a.l.T("domain." + string(d)) }

func (a *app) qualityName(q source.Quality) string { return a.l.T("quality." + string(q)) }

func (a *app) writeStatus(cmd *cobra.Command, st api.Status) error {
	l := a.l
	if a.out() != output.Table {
		return output.Write(cmd.OutOrStdout(), a.out(), st, a.statusGrid(st), a.backendsGrid(st.Backends))
	}
	w := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(w, "%s %s, %s %s, %s %d\n",
		l.T("cli.status.agent"), st.Version,
		l.T("cli.status.uptime"), since(st.StartedAt),
		l.T("cli.status.samples"), st.Samples)
	_, _ = fmt.Fprintf(w, "%s %s, %s %s\n\n", l.T("cli.status.interval"), time.Duration(st.IntervalMS)*time.Millisecond,
		l.T("cli.status.policy"), st.IdlePolicy)
	if err := output.WriteGrid(w, a.statusGrid(st)); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(w)
	if len(st.Backends) == 0 {
		_, _ = fmt.Fprintln(w, l.T("cli.status.no_backends"))
	} else if err := output.WriteGrid(w, a.backendsGrid(st.Backends)); err != nil {
		return err
	}
	if st.Calibration != nil {
		c := st.Calibration
		_, _ = fmt.Fprintf(w, "\n%s %s (%s)\n", l.T("cli.status.baseline"), units.Power(c.Baseline.CPUW, a.raw), c.At.Local().Format(time.DateTime))
		if c.Drift > 0.2 {
			_, _ = fmt.Fprintln(w, a.paint(l.T("cli.status.drift", "idle", units.Power(c.IdleNowW, a.raw), "percent", fmt.Sprintf("%.0f", c.Drift*100)), true))
		}
	} else {
		_, _ = fmt.Fprintln(w, "\n"+a.paint(l.T("cli.status.no_calibration"), true))
	}
	_, _ = fmt.Fprintf(w, "%s %s, %s %.1f MiB\n", l.T("cli.status.self"), units.Energy(st.Self.EnergyJ, a.raw),
		l.T("cli.status.memory"), float64(st.Self.RSSBytes)/(1<<20))
	return a.writeProblems(w, st.Problems)
}

func (a *app) statusGrid(st api.Status) output.Grid {
	l := a.l
	g := output.Grid{Header: []string{l.T("cli.col.domain"), l.T("cli.col.power"), l.T("cli.col.energy_since_start"), l.T("cli.col.quality")}}
	for _, d := range st.Domains {
		name := a.domainName(d.Domain)
		if d.Level != "component" {
			name += " (" + l.T("level."+d.Level) + ")"
		}
		g.Rows = append(g.Rows, []string{name, units.Power(d.PowerW, a.raw), units.Energy(d.EnergyJ, a.raw), a.qualityName(d.Quality)})
	}
	return g
}

// backendsGrid lists every backend with its models below it.
func (a *app) backendsGrid(bs []api.Backend) output.Grid {
	l := a.l
	g := output.Grid{Header: []string{l.T("cli.col.backend"), l.T("cli.col.kind"), l.T("cli.col.power"), l.T("cli.col.energy_since_start"), l.T("cli.col.tokens"), l.T("cli.col.joules_per_token")}}
	for _, b := range bs {
		g.Rows = append(g.Rows, []string{b.Name, b.Kind, units.Power(b.PowerW, a.raw), units.Energy(b.EnergyJ, a.raw), "", ""})
		for _, m := range b.Models {
			tokens, jpt := l.T("cli.status.not_counted"), "-"
			if b.TokenCounters || m.PromptTokens+m.GeneratedTokens > 0 {
				tokens = fmt.Sprintf("%d / %d", m.PromptTokens, m.GeneratedTokens)
			}
			if m.JoulesPerToken > 0 {
				jpt = units.Energy(m.JoulesPerToken, a.raw)
			}
			g.Rows = append(g.Rows, []string{"  " + m.Name, "", "", units.Energy(m.EnergyJ, a.raw), tokens, jpt})
		}
	}
	return g
}

// brief is what greenlens prints without arguments: one line of state and
// the next step to take.
func (a *app) brief(parent context.Context, cmd *cobra.Command) error {
	l := a.l
	w := cmd.OutOrStdout()
	cfg, _, err := a.config()
	if err != nil {
		cfg = config.Defaults()
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	var line, next string
	if c, err := a.agent(ctx, cfg); err != nil {
		line, next = l.T("cli.brief.no_agent"), l.T("cli.brief.next_agent")
	} else if st, err := c.Status(ctx); err != nil {
		line, next = l.T("cli.brief.no_agent"), l.T("cli.brief.next_agent")
	} else {
		var watts float64
		for _, d := range st.Domains {
			if d.Level == "component" {
				watts += d.PowerW
			}
		}
		q := l.T("quality.measured")
		if st.Estimated {
			q = l.T("quality.estimated")
		}
		line = l.T("cli.brief.running", "power", units.Power(watts, a.raw), "quality", q)
		next = l.T("cli.brief.next_status")
		if st.Calibration == nil {
			next = l.T("cli.brief.next_calibrate")
		}
	}
	_, err = fmt.Fprintf(w, "%s\n%s\n\n%s\n", line, next, l.T("cli.brief.more"))
	return err
}

// writeProblems lists coded problems with their fixes.
func (a *app) writeProblems(w interface{ Write([]byte) (int, error) }, ps []api.Problem) error {
	if len(ps) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(w, "\n%s\n", a.l.T("cli.problems")); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, p := range ps {
		e := p.Error()
		key := string(p.Code) + e.Message(a.l) + e.Cause(a.l)
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, err := fmt.Fprintln(w, a.paint(help.Render(a.l, e), true)); err != nil {
			return err
		}
	}
	return nil
}
