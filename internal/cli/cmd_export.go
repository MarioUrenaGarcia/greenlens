// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/bench"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/output"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

// maxExportBuckets bounds the requests of a usage export, one per bucket.
const maxExportBuckets = 1000

// UsageExportRow is the usage of one group in one bucket.
type UsageExportRow struct {
	BucketStart time.Time `json:"bucket_start"`
	store.UsageEnergy
}

// UsageExport is the document of greenlens export usage.
type UsageExport struct {
	SchemaVersion int              `json:"schema_version"`
	From          time.Time        `json:"from"`
	To            time.Time        `json:"to"`
	GroupBy       string           `json:"group_by"`
	BucketMS      int64            `json:"bucket_ms"`
	Resolution    string           `json:"resolution"`
	Rows          []UsageExportRow `json:"rows"`
}

func newExportCmd(a *app) *cobra.Command {
	l := a.l
	cmd := &cobra.Command{
		Use:   "export",
		Short: l.T("cli.export.short"),
		Long:  wrap(l.T("cli.export.long"), helpWidth),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownCommand(cmd, args[0])
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newExportEnergyCmd(a), newExportUsageCmd(a), newExportBenchmarksCmd(a))
	return cmd
}

// exportFormat writes CSV unless JSON or YAML was asked for, since an
// aligned table is not an export.
func (a *app) exportFormat() output.Format {
	if f := a.out(); f != output.Table {
		return f
	}
	return output.CSV
}

// period parses --from and --to.
func period(cmd *cobra.Command, fromS, toS string) (time.Time, time.Time, error) {
	now := time.Now()
	from, err := parseTime(fromS, now)
	if err != nil {
		return time.Time{}, time.Time{}, help.New(help.GL0005, "flag", "--from", "value", fromS, "command", cmd.CommandPath())
	}
	to, err := parseTime(toS, now)
	if err != nil || !from.Before(to) {
		return time.Time{}, time.Time{}, help.New(help.GL0005, "flag", "--to", "value", toS, "command", cmd.CommandPath())
	}
	return from, to, nil
}

// bucketFlag parses --bucket: "none" for one total, or a duration of at
// least a minute.
func bucketFlag(cmd *cobra.Command, s string) (time.Duration, error) {
	if s == "none" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < time.Minute {
		return 0, help.New(help.GL0005, "flag", "--bucket", "value", s, "command", cmd.CommandPath())
	}
	return d, nil
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func float(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

func newExportEnergyCmd(a *app) *cobra.Command {
	l := a.l
	var fromS, toS, bucketS string
	cmd := &cobra.Command{
		Use:   "energy",
		Short: l.T("cli.export.energy.short"),
		Long:  wrap(l.T("cli.export.energy.long"), helpWidth),
		Example: "  greenlens export energy --from 7d > energy.csv\n" +
			"  greenlens export energy --from 2026-10-01 --to 2026-11-01 --bucket 1m --output json",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			from, to, err := period(cmd, fromS, toS)
			if err != nil {
				return err
			}
			bucket, err := bucketFlag(cmd, bucketS)
			if err != nil {
				return err
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
			if u.Rows == nil {
				u.Rows = []store.ChannelEnergy{}
			}
			g := output.Grid{Header: []string{"bucket_start", "channel", "source", "domain", "quality", "energy_j", "samples"}}
			for _, r := range u.Rows {
				g.Rows = append(g.Rows, []string{rfc3339(r.BucketStart), r.Channel, r.Source, string(r.Domain), r.Quality,
					float(r.EnergyJ), strconv.FormatInt(r.Samples, 10)})
			}
			return output.Write(cmd.OutOrStdout(), a.exportFormat(), u, g)
		},
	}
	cmd.Flags().StringVar(&fromS, "from", "24h", l.T("cli.report.flag.from"))
	cmd.Flags().StringVar(&toS, "to", "now", l.T("cli.report.flag.to"))
	cmd.Flags().StringVar(&bucketS, "bucket", "1h", l.T("cli.export.flag.bucket"))
	return cmd
}

func newExportUsageCmd(a *app) *cobra.Command {
	l := a.l
	var fromS, toS, bucketS, group string
	cmd := &cobra.Command{
		Use:   "usage",
		Short: l.T("cli.export.usage.short"),
		Long:  wrap(l.T("cli.export.usage.long"), helpWidth),
		Example: "  greenlens export usage --from 30d --group consumer > consumers.csv\n" +
			"  greenlens export usage --from 7d --bucket 24h --group model --output json",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			from, to, err := period(cmd, fromS, toS)
			if err != nil {
				return err
			}
			bucket, err := bucketFlag(cmd, bucketS)
			if err != nil {
				return err
			}
			if bucket > 0 && to.Sub(from)/bucket >= maxExportBuckets {
				return help.New(help.GL0005, "flag", "--bucket", "value", bucketS, "command", cmd.CommandPath())
			}
			if group != store.ByBackend && group != store.ByModel && group != store.ByConsumer {
				return help.New(help.GL0005, "flag", "--group", "value", group, "command", cmd.CommandPath())
			}
			cfg, _, err := a.config()
			if err != nil {
				return err
			}
			c, err := a.agent(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			doc := UsageExport{SchemaVersion: 1, From: from, To: to, GroupBy: group, BucketMS: bucket.Milliseconds(), Rows: []UsageExportRow{}}
			step := bucket
			if step == 0 {
				step = to.Sub(from)
			}
			for start := from; start.Before(to); start = start.Add(step) {
				end := start.Add(step)
				if end.After(to) {
					end = to
				}
				u, err := c.BackendUsage(cmd.Context(), start, end, group)
				if err != nil {
					return err
				}
				// Older buckets come first and are read at the coarsest
				// resolution, which is the one the whole export can claim.
				if doc.Resolution == "" {
					doc.Resolution = u.Resolution
				}
				for _, r := range u.Rows {
					doc.Rows = append(doc.Rows, UsageExportRow{BucketStart: start, UsageEnergy: r})
				}
			}
			g := output.Grid{Header: []string{"bucket_start", "backend", "kind", "model", "consumer", "energy_j",
				"prompt_tokens", "generated_tokens", "requests", "tokens_counted"}}
			for _, r := range doc.Rows {
				g.Rows = append(g.Rows, []string{rfc3339(r.BucketStart), r.Backend, r.Kind, r.Model, r.Consumer, float(r.EnergyJ),
					strconv.FormatInt(r.PromptTokens, 10), strconv.FormatInt(r.GeneratedTokens, 10),
					strconv.FormatInt(r.Requests, 10), strconv.FormatBool(r.Counted)})
			}
			return output.Write(cmd.OutOrStdout(), a.exportFormat(), doc, g)
		},
	}
	cmd.Flags().StringVar(&fromS, "from", "24h", l.T("cli.report.flag.from"))
	cmd.Flags().StringVar(&toS, "to", "now", l.T("cli.report.flag.to"))
	cmd.Flags().StringVar(&bucketS, "bucket", "none", l.T("cli.export.flag.bucket"))
	cmd.Flags().StringVar(&group, "group", store.ByModel, l.T("cli.export.flag.group"))
	return cmd
}

func newExportBenchmarksCmd(a *app) *cobra.Command {
	l := a.l
	var limit int
	cmd := &cobra.Command{
		Use:   "benchmarks",
		Short: l.T("cli.export.benchmarks.short"),
		Long:  wrap(l.T("cli.export.benchmarks.long"), helpWidth),
		Example: "  greenlens export benchmarks > benchmarks.csv\n" +
			"  greenlens export benchmarks --limit 10 --output json",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit < 1 || limit > 1000 {
				return help.New(help.GL0005, "flag", "--limit", "value", limit, "command", cmd.CommandPath())
			}
			cfg, _, err := a.config()
			if err != nil {
				return err
			}
			c, err := a.agent(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			list, err := c.Benchmarks(cmd.Context(), limit)
			if err != nil {
				return err
			}
			if list.Benchmarks == nil {
				list.Benchmarks = []bench.Result{}
			}
			g := output.Grid{Header: []string{"id", "started_at", "ended_at", "kind", "model", "basis", "calibrated", "runs",
				"prompt_set_sha256", "generated_tokens", "j_per_token_mean", "j_per_token_stddev", "j_per_token_cv", "j_per_token_ci95", "energy_j_mean"}}
			for _, b := range list.Benchmarks {
				g.Rows = append(g.Rows, []string{strconv.FormatInt(b.ID, 10), rfc3339(b.StartedAt), rfc3339(b.EndedAt),
					string(b.Target.Kind), b.Target.Model, b.Basis, strconv.FormatBool(b.Calibrated), strconv.Itoa(len(b.Runs)),
					b.PromptSetSHA256, strconv.FormatInt(b.GeneratedTokens, 10), float(b.JPerToken.Mean), float(b.JPerToken.StdDev),
					float(b.JPerToken.CV), float(b.JPerToken.CI95), float(b.Energy.Mean)})
			}
			return output.Write(cmd.OutOrStdout(), a.exportFormat(), list, g)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 1000, l.T("cli.export.flag.limit"))
	return cmd
}
