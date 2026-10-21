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

	"github.com/MarioUrenaGarcia/greenlens/internal/analysis"
	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/output"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
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
	HourlyProfile  bool        `json:"hourly_profile"`
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
	var fromS, toS, by, group string
	var regressions bool
	cmd := &cobra.Command{
		Use:   "report",
		Short: l.T("cli.report.short"),
		Long:  wrap(l.T("cli.report.long"), helpWidth),
		Example: "  greenlens report\n  greenlens report --from 7d --by day\n  greenlens report --from 7d --group model\n" +
			"  greenlens report --from 30d --regressions\n  greenlens report --from 2026-10-01 --to 2026-11-01 --output csv",
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
			bucket, ok := map[string]time.Duration{"none": 0, "hour": time.Hour, "day": 24 * time.Hour}[by]
			if !ok {
				return help.New(help.GL0005, "flag", "--by", "value", by, "command", cmd.CommandPath())
			}
			if group != "" && (group != store.ByBackend && group != store.ByModel && group != store.ByConsumer || by != "none") {
				return help.New(help.GL0005, "flag", "--group", "value", group, "command", cmd.CommandPath())
			}
			if regressions && (group != "" || by != "none") {
				return help.New(help.GL0005, "flag", "--regressions", "value", "true", "command", cmd.CommandPath())
			}
			cfg, _, err := a.config()
			if err != nil {
				return err
			}
			c, err := a.agent(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			if regressions {
				rg, err := c.Regressions(cmd.Context(), from, to)
				if err != nil {
					return err
				}
				return a.writeRegressions(cmd, rg)
			}
			// The indicators come from the agent, computed hour by hour
			// with the factors of its own configuration.
			st, err := c.Status(cmd.Context())
			if err != nil {
				return err
			}
			f := st.Factors
			if group != "" {
				u, err := c.BackendUsage(cmd.Context(), from, to, group)
				if err != nil {
					return err
				}
				return a.writeBackendReport(cmd, backendReport(u, f))
			}
			u, err := c.Usage(cmd.Context(), from, to, bucket)
			if err != nil {
				return err
			}
			rep := Report{SchemaVersion: 1, From: from, To: to, Bucket: by, Resolution: u.Resolution,
				EmissionGKWh: f.EmissionGPerKWh, EmissionSource: f.EmissionSource, HourlyProfile: f.HourlyProfile,
				Total: ReportRow{Start: from, DomainsJ: map[source.Domain]float64{}}}
			if f.TariffPerKWh != nil || f.TariffPeriods > 0 {
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
				// Platform and wall meters measure the components again.
				if r.Domain.Level() == source.LevelComponent && r.Indicators != nil {
					row.Indicators.Add(*r.Indicators)
					rep.Total.Indicators.Add(*r.Indicators)
				}
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
	cmd.Flags().StringVar(&group, "group", "", l.T("cli.report.flag.group"))
	cmd.Flags().BoolVar(&regressions, "regressions", false, l.T("cli.report.flag.regressions"))
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

// BackendReportRow is the energy of one backend, or one model, in a period.
type BackendReportRow struct {
	Backend         string            `json:"backend"`
	Kind            string            `json:"kind"`
	Model           string            `json:"model,omitempty"`
	Consumer        string            `json:"consumer,omitempty"`
	EnergyJ         float64           `json:"energy_j"`
	PromptTokens    int64             `json:"prompt_tokens"`
	GeneratedTokens int64             `json:"generated_tokens"`
	Requests        int64             `json:"requests"`
	TokensCounted   bool              `json:"tokens_counted"`
	JoulesPerToken  float64           `json:"joules_per_generated_token"`
	Indicators      indicators.Values `json:"indicators"`
}

// BackendReport is the document of greenlens report --group.
type BackendReport struct {
	SchemaVersion  int                `json:"schema_version"`
	From           time.Time          `json:"from"`
	To             time.Time          `json:"to"`
	GroupBy        string             `json:"group_by"`
	Resolution     string             `json:"resolution"`
	EmissionGKWh   float64            `json:"emission_factor_g_per_kwh"`
	EmissionSource string             `json:"emission_factor_source"`
	Currency       string             `json:"currency,omitempty"`
	Rows           []BackendReportRow `json:"rows"`
}

func backendReport(u api.BackendUsage, f api.Factors) BackendReport {
	rep := BackendReport{SchemaVersion: 1, From: u.From, To: u.To, GroupBy: u.GroupBy, Resolution: u.Resolution,
		EmissionGKWh: f.EmissionGPerKWh, EmissionSource: f.EmissionSource, Rows: []BackendReportRow{}}
	if f.TariffPerKWh != nil || f.TariffPeriods > 0 {
		rep.Currency = f.Currency
	}
	for _, r := range u.Rows {
		row := BackendReportRow{Backend: r.Backend, Kind: r.Kind, Model: r.Model, Consumer: r.Consumer, EnergyJ: r.EnergyJ,
			PromptTokens: r.PromptTokens, GeneratedTokens: r.GeneratedTokens, Requests: r.Requests, TokensCounted: r.Counted}
		if r.Indicators != nil {
			row.Indicators = *r.Indicators
		}
		if r.GeneratedTokens > 0 {
			row.JoulesPerToken = r.EnergyJ / float64(r.GeneratedTokens)
		}
		rep.Rows = append(rep.Rows, row)
	}
	return rep
}

func (a *app) writeBackendReport(cmd *cobra.Command, rep BackendReport) error {
	l := a.l
	header := []string{l.T("cli.col.backend")}
	switch rep.GroupBy {
	case store.ByModel:
		header = append(header, l.T("cli.col.model"))
	case store.ByConsumer:
		header = append(header, l.T("cli.col.consumer"))
	}
	header = append(header, l.T("cli.col.energy"), "kWh", "CO2e")
	if rep.Currency != "" {
		header = append(header, l.T("cli.col.cost"))
	}
	header = append(header, l.T("cli.col.tokens"), l.T("cli.col.joules_per_token"))
	g := output.Grid{Header: header}
	for _, r := range rep.Rows {
		row := []string{r.Backend}
		if rep.GroupBy == store.ByModel || rep.GroupBy == store.ByConsumer {
			v := r.Model
			if rep.GroupBy == store.ByConsumer {
				v = r.Consumer
			}
			if v == "" {
				v = "-"
			}
			row = append(row, v)
		}
		row = append(row, units.Energy(r.EnergyJ, a.raw), strconv.FormatFloat(r.Indicators.KWh, 'f', 4, 64), units.CO2e(r.Indicators.CO2eG, a.raw))
		if rep.Currency != "" {
			cost := "-"
			if r.Indicators.Cost != nil {
				cost = units.Money(*r.Indicators.Cost, rep.Currency, a.raw)
			}
			row = append(row, cost)
		}
		tokens, jpt := l.T("cli.status.not_counted"), "-"
		if r.TokensCounted {
			tokens = fmt.Sprintf("%d / %d", r.PromptTokens, r.GeneratedTokens)
		}
		if r.JoulesPerToken > 0 {
			jpt = units.Energy(r.JoulesPerToken, a.raw)
		}
		g.Rows = append(g.Rows, append(row, tokens, jpt))
	}
	if a.out() != output.Table {
		return output.Write(cmd.OutOrStdout(), a.out(), rep, g)
	}
	w := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(w, l.T("cli.report.period", "from", rep.From.Local().Format(time.DateTime), "to", rep.To.Local().Format(time.DateTime)))
	if len(rep.Rows) == 0 {
		_, err := fmt.Fprintln(w, l.T("cli.report.no_usage"))
		return err
	}
	if err := output.WriteGrid(w, g); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w, "\n"+l.T("cli.report.factor", "value", strconv.FormatFloat(rep.EmissionGKWh, 'f', -1, 64), "source", rep.EmissionSource))
	return err
}

// writeRegressions lists the days that are regressions or improvements,
// and the changes of context of the period.
func (a *app) writeRegressions(cmd *cobra.Command, rg api.Regressions) error {
	l := a.l
	g := output.Grid{Header: []string{l.T("cli.regress.col.day"), l.T("cli.col.backend"), l.T("cli.col.model"),
		l.T("cli.col.joules_per_token"), l.T("cli.regress.col.baseline"), "z", l.T("cli.regress.col.status")}}
	for _, m := range rg.Models {
		for _, d := range m.Days {
			if d.Status != analysis.StatusRegression && d.Status != analysis.StatusImprovement {
				continue
			}
			g.Rows = append(g.Rows, []string{d.Day, m.Backend, m.Model, units.Energy(d.JPerToken, a.raw),
				units.Energy(d.BaselineMean, a.raw) + " +- " + units.Energy(d.BaselineSD, a.raw),
				strconv.FormatFloat(*d.Z, 'f', 2, 64), l.T("cli.regress.status." + d.Status)})
		}
	}
	if a.out() != output.Table {
		return output.Write(cmd.OutOrStdout(), a.out(), rg, g)
	}
	w := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(w, l.T("cli.report.period", "from", rg.From.Local().Format(time.DateTime), "to", rg.To.Local().Format(time.DateTime)))
	_, _ = fmt.Fprintln(w)
	if len(g.Rows) == 0 {
		_, _ = fmt.Fprintln(w, wrap(l.T("cli.regress.none", "z", strconv.FormatFloat(rg.Params.Z, 'f', -1, 64), "days", strconv.Itoa(rg.Params.BaselineDays)), helpWidth))
	} else if err := output.WriteGrid(w, g); err != nil {
		return err
	}
	if len(rg.Events) > 0 {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, l.T("cli.regress.changes"))
		for _, e := range rg.Events {
			what := l.T("cli.regress.kind." + e.Kind)
			if e.Subject != "" {
				what += " " + e.Subject
			}
			change := e.Value
			if e.Previous != "" {
				change = e.Previous + " -> " + e.Value
			}
			_, _ = fmt.Fprintf(w, "  %s  %s: %s\n", e.At.Local().Format(time.DateTime), what, change)
		}
	}
	return nil
}
