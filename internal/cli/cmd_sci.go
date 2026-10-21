// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/output"
	"github.com/MarioUrenaGarcia/greenlens/internal/report"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
	"github.com/MarioUrenaGarcia/greenlens/internal/units"
)

func newSCICmd(a *app) *cobra.Command {
	l := a.l
	var fromS, toS, group, unit, htmlPath string
	cmd := &cobra.Command{
		Use:   "sci",
		Short: l.T("cli.sci.short"),
		Long:  wrap(l.T("cli.sci.long"), helpWidth),
		Example: "  greenlens sci --from 30d\n  greenlens sci --from 7d --group model\n" +
			"  greenlens sci --from 2026-10-01 --to 2026-11-01 --unit request --output csv",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			now := time.Now()
			from, err := parseTime(fromS, now)
			if err != nil {
				return help.New(help.GL0005, "flag", "--from", "value", fromS, "command", cmd.CommandPath())
			}
			to, err := parseTime(toS, now)
			if err != nil || !from.Before(to) {
				return help.New(help.GL0005, "flag", "--to", "value", toS, "command", cmd.CommandPath())
			}
			if group != "" && group != store.ByBackend && group != store.ByModel && group != store.ByConsumer {
				return help.New(help.GL0005, "flag", "--group", "value", group, "command", cmd.CommandPath())
			}
			if unit != "" && !slices.Contains(indicators.FunctionalUnits, unit) {
				return help.New(help.GL0005, "flag", "--unit", "value", unit, "command", cmd.CommandPath())
			}
			cfg, _, err := a.config()
			if err != nil {
				return err
			}
			c, err := a.agent(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			if htmlPath != "" {
				return a.savePage(cmd, c, "sci", from, to, group, unit, htmlPath)
			}
			rep, err := c.SCIReport(cmd.Context(), from, to, group, unit)
			if err != nil {
				return err
			}
			return a.writeSCI(cmd, rep)
		},
	}
	cmd.Flags().StringVar(&fromS, "from", "24h", l.T("cli.report.flag.from"))
	cmd.Flags().StringVar(&toS, "to", "now", l.T("cli.report.flag.to"))
	cmd.Flags().StringVar(&group, "group", "", l.T("cli.sci.flag.group"))
	cmd.Flags().StringVar(&unit, "unit", "", l.T("cli.sci.flag.unit"))
	cmd.Flags().StringVar(&htmlPath, "html", "", l.T("cli.report.flag.html"))
	return cmd
}

func (a *app) writeSCI(cmd *cobra.Command, rep report.SCIReport) error {
	l := a.l
	header := []string{l.T("cli.sci.col.scope"), l.T("cli.col.energy"), l.T("cli.sci.col.operational"),
		l.T("cli.sci.col.embodied"), l.T("cli.sci.col.units"), "SCI"}
	g := output.Grid{Header: header}
	opt := func(v *float64, f func(float64) string) string {
		if v == nil {
			return "-"
		}
		return f(*v)
	}
	add := func(scope string, r report.SCIRow) {
		g.Rows = append(g.Rows, []string{scope, units.Energy(r.EnergyJ, a.raw), units.CO2e(r.SCI.OperationalG, a.raw),
			opt(r.SCI.EmbodiedG, func(v float64) string { return units.CO2e(v, a.raw) }),
			strconv.FormatFloat(r.SCI.Units, 'f', -1, 64),
			opt(r.SCI.GPerUnit, func(v float64) string { return units.CO2e(v, a.raw) })})
	}
	add(l.T("cli.sci.node"), rep.Node)
	for _, r := range rep.Rows {
		scope := r.Backend
		switch rep.GroupBy {
		case store.ByModel:
			scope += "/" + dash(r.Model)
		case store.ByConsumer:
			scope += "/" + dash(r.Consumer)
		}
		add(scope, r)
	}
	if a.out() != output.Table {
		return output.Write(cmd.OutOrStdout(), a.out(), rep, g)
	}
	w := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(w, l.T("cli.report.period", "from", rep.From.Local().Format(time.DateTime), "to", rep.To.Local().Format(time.DateTime)))
	_, _ = fmt.Fprintln(w, l.T("cli.sci.unit."+rep.Unit))
	_, _ = fmt.Fprintln(w)
	if err := output.WriteGrid(w, g); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(w)
	if len(rep.Missing) > 0 {
		_, _ = fmt.Fprintln(w, wrap(l.T("cli.sci.missing", "keys", strings.Join(rep.Missing, ", ")), helpWidth))
	}
	_, err := fmt.Fprintln(w, wrap(l.T("cli.sci.method"), helpWidth))
	return err
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
