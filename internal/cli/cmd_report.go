// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/output"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/units"
)

// ReportRow is the energy of one period.
type ReportRow struct {
	Start       time.Time                 `json:"start"`
	DomainsJ    map[source.Domain]float64 `json:"domains_j"`
	ComponentsJ float64                   `json:"components_j"`
	Indicators  indicators.Values         `json:"indicators"`
}

// Report is the document of greenlens report.
type Report struct {
	SchemaVersion  int         `json:"schema_version"`
	From           time.Time   `json:"from"`
	To             time.Time   `json:"to"`
	Bucket         string      `json:"bucket"`
	Resolution     string      `json:"resolution"`
	Estimated      bool        `json:"estimated"`
	EmissionGKWh   float64     `json:"emission_factor_g_per_kwh"`
	EmissionSource string      `json:"emission_factor_source"`
	Currency       string      `json:"currency,omitempty"`
	Rows           []ReportRow `json:"rows"`
	Total          ReportRow   `json:"total"`
}

var relative = regexp.MustCompile(`^(\d+)([mhdw])$`)

// parseTime accepts "now", RFC 3339 times, dates, and amounts of time ago
// such as 30m, 24h, 7d or 2w.
func parseTime(s string, now time.Time) (time.Time, error) {
	switch {
	case s == "now":
		return now, nil
	case relative.MatchString(s):
		m := relative.FindStringSubmatch(s)
		n, _ := strconv.Atoi(m[1])
		unit := map[string]time.Duration{"m": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}[m[2]]
		return now.Add(-time.Duration(n) * unit), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.ParseInLocation(time.DateOnly, s, time.Local)
}

func newReportCmd(a *app) *cobra.Command {
	l := a.l
	var fromS, toS, by string
	cmd := &cobra.Command{
		Use:     "report",
		Short:   l.T("cli.report.short"),
		Long:    wrap(l.T("cli.report.long"), helpWidth),
		Example: "  greenlens report\n  greenlens report --from 7d --by day\n  greenlens report --from 2026-10-01 --to 2026-11-01 --output csv",
		Args:    noArgs,
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
			bucket, ok := map[string]time.Duration{"none": 0, "hour": time.Hour, "day": 24 * time.Hour}[by]
			if !ok {
				return help.New(help.GL0005, "flag", "--by", "value", by, "command", cmd.CommandPath())
			}
			cfg, _, err := a.config()
			if err != nil {
				return err
			}
			c, err := a.agent(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			u, err := c.Usage(cmd.Context(), from, to, bucket)
			if err != nil {
				return err
			}
			f := cfg.Factors()
			rep := Report{SchemaVersion: 1, From: from, To: to, Bucket: by, Resolution: u.Resolution,
				EmissionGKWh: f.EmissionGPerKWh, EmissionSource: f.EmissionSource,
				Total: ReportRow{Start: from, DomainsJ: map[source.Domain]float64{}}}
			if f.TariffPerKWh != nil {
				rep.Currency = f.Currency
			}
			byStart := map[time.Time]*ReportRow{}
			for _, r := range u.Rows {
				row := byStart[r.BucketStart]
				if row == nil {
					row = &ReportRow{Start: r.BucketStart, DomainsJ: map[source.Domain]float64{}}
					byStart[r.BucketStart] = row
				}
				row.DomainsJ[r.Domain] += r.EnergyJ
				rep.Total.DomainsJ[r.Domain] += r.EnergyJ
				if r.Quality == string(source.QualityEstimated) {
					rep.Estimated = true
				}
			}
			for _, row := range byStart {
				rep.Rows = append(rep.Rows, *row)
			}
			slices.SortFunc(rep.Rows, func(x, y ReportRow) int { return x.Start.Compare(y.Start) })
			finish := func(r *ReportRow) {
				for d, j := range r.DomainsJ {
					if d.Level() == source.LevelComponent {
						r.ComponentsJ += j
					}
				}
				r.Indicators = f.Compute(r.ComponentsJ)
			}
			for i := range rep.Rows {
				finish(&rep.Rows[i])
			}
			finish(&rep.Total)
			return a.writeReport(cmd, rep)
		},
	}
	cmd.Flags().StringVar(&fromS, "from", "24h", l.T("cli.report.flag.from"))
	cmd.Flags().StringVar(&toS, "to", "now", l.T("cli.report.flag.to"))
	cmd.Flags().StringVar(&by, "by", "none", l.T("cli.report.flag.by"))
	return cmd
}

func (a *app) writeReport(cmd *cobra.Command, rep Report) error {
	l := a.l
	present := map[source.Domain]bool{}
	for d := range rep.Total.DomainsJ {
		present[d] = true
	}
	cols := []source.Domain{}
	for _, d := range []source.Domain{source.DomainPackage, source.DomainDRAM, source.DomainGPU, source.DomainPSys, source.DomainPlatform, source.DomainWall} {
		if present[d] {
			cols = append(cols, d)
		}
	}
	header := []string{l.T("cli.col.period")}
	for _, d := range cols {
		header = append(header, a.domainName(d))
	}
	header = append(header, l.T("cli.col.components"), "kWh", "CO2e")
	if rep.Currency != "" {
		header = append(header, l.T("cli.col.cost"))
	}
	g := output.Grid{Header: header}
	add := func(label string, r ReportRow) {
		row := []string{label}
		for _, d := range cols {
			row = append(row, units.Energy(r.DomainsJ[d], a.raw))
		}
		row = append(row, units.Energy(r.ComponentsJ, a.raw), strconv.FormatFloat(r.Indicators.KWh, 'f', 4, 64), units.CO2e(r.Indicators.CO2eG, a.raw))
		if rep.Currency != "" && r.Indicators.Cost != nil {
			row = append(row, units.Money(*r.Indicators.Cost, rep.Currency, a.raw))
		}
		g.Rows = append(g.Rows, row)
	}
	layout := time.DateTime
	if rep.Bucket == "day" {
		layout = time.DateOnly
	}
	if rep.Bucket != "none" {
		for _, r := range rep.Rows {
			add(r.Start.Local().Format(layout), r)
		}
	}
	add(l.T("cli.report.total"), rep.Total)
	if a.out() != output.Table {
		return output.Write(cmd.OutOrStdout(), a.out(), rep, g)
	}
	w := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(w, l.T("cli.report.period", "from", rep.From.Local().Format(time.DateTime), "to", rep.To.Local().Format(time.DateTime)))
	if err := output.WriteGrid(w, g); err != nil {
		return err
	}
	notes := []string{l.T("cli.report.factor", "value", strconv.FormatFloat(rep.EmissionGKWh, 'f', -1, 64), "source", rep.EmissionSource)}
	if rep.Estimated {
		notes = append(notes, a.paint(l.T("cli.report.estimated"), true))
	}
	if present[source.DomainPlatform] || present[source.DomainPSys] || present[source.DomainWall] {
		notes = append(notes, l.T("cli.report.system_levels"))
	}
	_, err := fmt.Fprintln(w, "\n"+strings.Join(notes, "\n"))
	return err
}
