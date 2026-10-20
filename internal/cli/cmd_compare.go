// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/bench"
	"github.com/MarioUrenaGarcia/greenlens/internal/client"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/output"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
	"github.com/MarioUrenaGarcia/greenlens/internal/units"
)

// CompareSide is one of the two things compared.
type CompareSide struct {
	Label           string  `json:"label"`
	Kind            string  `json:"kind"`
	JPerToken       float64 `json:"j_per_generated_token"`
	CI95            float64 `json:"ci95,omitempty"`
	GeneratedTokens int64   `json:"generated_tokens"`
	EnergyJ         float64 `json:"energy_j"`
	Basis           string  `json:"basis,omitempty"`
}

// CompareReport is the document of greenlens compare.
type CompareReport struct {
	SchemaVersion int         `json:"schema_version"`
	A             CompareSide `json:"a"`
	B             CompareSide `json:"b"`
	// Ratio is the energy per token of B over that of A.
	Ratio float64 `json:"ratio"`
	// Tokens is the work projected: the tokens A generated in the period,
	// or 1,000 for benchmarks.
	Tokens         int64             `json:"projected_tokens"`
	EnergyAJ       float64           `json:"projected_energy_a_j"`
	EnergyBJ       float64           `json:"projected_energy_b_j"`
	SavedJ         float64           `json:"saved_j"`
	SavedPercent   float64           `json:"saved_percent"`
	Saved          indicators.Values `json:"saved_indicators"`
	WithinNoise    bool              `json:"within_uncertainty"`
	Notes          []string          `json:"notes,omitempty"`
	EmissionSource string            `json:"emission_factor_source"`
	Currency       string            `json:"currency,omitempty"`
}

func newCompareCmd(a *app) *cobra.Command {
	l := a.l
	var fromS, toS string
	cmd := &cobra.Command{
		Use:     "compare <a> <b>",
		Short:   l.T("cli.compare.short"),
		Long:    wrap(l.T("cli.compare.long"), helpWidth),
		Example: "  greenlens compare 3 7\n  greenlens compare ollama-11434/llama3.2 ollama-11434/qwen2.5:0.5b --from 7d",
		Args:    exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			now := time.Now()
			from, err := parseTime(fromS, now)
			if err != nil {
				return help.New(help.GL0005, "flag", "--from", "value", fromS, "command", cmd.CommandPath())
			}
			to, err := parseTime(toS, now)
			if err != nil || !from.Before(to) {
				return help.New(help.GL0005, "flag", "--to", "value", toS, "command", cmd.CommandPath())
			}
			cfg, _, err := a.config()
			if err != nil {
				return err
			}
			c, err := a.agent(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			ia, errA := strconv.ParseInt(args[0], 10, 64)
			ib, errB := strconv.ParseInt(args[1], 10, 64)
			var rep CompareReport
			if errA == nil && errB == nil {
				rep, err = a.compareBenchmarks(cmd, c, ia, ib)
			} else {
				rep, err = a.compareModels(cmd, c, args[0], args[1], from, to)
			}
			if err != nil {
				return err
			}
			f := cfg.Factors()
			rep.Saved = f.Compute(rep.SavedJ)
			rep.EmissionSource = f.EmissionSource
			if f.TariffPerKWh != nil {
				rep.Currency = f.Currency
			}
			return a.writeCompare(cmd, rep)
		},
	}
	cmd.Flags().StringVar(&fromS, "from", "7d", l.T("cli.report.flag.from"))
	cmd.Flags().StringVar(&toS, "to", "now", l.T("cli.report.flag.to"))
	return cmd
}

func project(rep *CompareReport) {
	if rep.A.JPerToken > 0 {
		rep.Ratio = rep.B.JPerToken / rep.A.JPerToken
	}
	rep.EnergyAJ = rep.A.JPerToken * float64(rep.Tokens)
	rep.EnergyBJ = rep.B.JPerToken * float64(rep.Tokens)
	rep.SavedJ = rep.EnergyAJ - rep.EnergyBJ
	if rep.EnergyAJ > 0 {
		rep.SavedPercent = 100 * rep.SavedJ / rep.EnergyAJ
	}
}

func (a *app) compareBenchmarks(cmd *cobra.Command, c *client.Client, ia, ib int64) (CompareReport, error) {
	ba, err := c.Benchmark(cmd.Context(), ia)
	if err != nil {
		return CompareReport{}, err
	}
	bb, err := c.Benchmark(cmd.Context(), ib)
	if err != nil {
		return CompareReport{}, err
	}
	side := func(b bench.Result) CompareSide {
		return CompareSide{Label: fmt.Sprintf("#%d %s", b.ID, b.Target.Model), Kind: "benchmark", JPerToken: b.JPerToken.Mean, CI95: b.JPerToken.CI95,
			GeneratedTokens: b.GeneratedTokens, EnergyJ: b.Energy.Mean * float64(len(b.Runs)), Basis: b.Basis}
	}
	rep := CompareReport{SchemaVersion: 1, A: side(ba), B: side(bb), Tokens: 1000}
	project(&rep)
	// Intervals that overlap leave the difference within the noise.
	rep.WithinNoise = math.Abs(rep.A.JPerToken-rep.B.JPerToken) <= rep.A.CI95+rep.B.CI95
	if ba.PromptSetSHA256 != bb.PromptSetSHA256 {
		rep.Notes = append(rep.Notes, "prompts")
	}
	if ba.Basis != bb.Basis {
		rep.Notes = append(rep.Notes, "basis")
	}
	if ba.Context.CPU != bb.Context.CPU || strings.Join(ba.Context.GPUs, ",") != strings.Join(bb.Context.GPUs, ",") {
		rep.Notes = append(rep.Notes, "hardware")
	}
	if ba.Params.MaxTokens != bb.Params.MaxTokens {
		rep.Notes = append(rep.Notes, "max_tokens")
	}
	return rep, nil
}

func (a *app) compareModels(cmd *cobra.Command, c *client.Client, ra, rb string, from, to time.Time) (CompareReport, error) {
	u, err := c.BackendUsage(cmd.Context(), from, to, store.ByModel)
	if err != nil {
		return CompareReport{}, err
	}
	find := func(ref string) (store.UsageEnergy, error) {
		backend, model, scoped := strings.Cut(ref, "/")
		if !scoped {
			model, backend = ref, ""
		}
		var hits []store.UsageEnergy
		for _, r := range u.Rows {
			if r.Model == model && (backend == "" || r.Backend == backend) {
				hits = append(hits, r)
			}
		}
		switch {
		case len(hits) == 0 || hits[0].GeneratedTokens == 0:
			return store.UsageEnergy{}, help.New(help.GL0005, "flag", "<a> <b>", "value", ref, "command", cmd.CommandPath())
		case len(hits) > 1:
			// The same model on several backends needs its backend.
			return store.UsageEnergy{}, help.New(help.GL0005, "flag", "<a> <b>", "value", ref+" ("+hits[0].Backend+"/"+model+", "+hits[1].Backend+"/"+model+")", "command", cmd.CommandPath())
		}
		return hits[0], nil
	}
	ua, err := find(ra)
	if err != nil {
		return CompareReport{}, err
	}
	ub, err := find(rb)
	if err != nil {
		return CompareReport{}, err
	}
	side := func(r store.UsageEnergy) CompareSide {
		return CompareSide{Label: r.Backend + "/" + r.Model, Kind: "model", JPerToken: r.EnergyJ / float64(r.GeneratedTokens), GeneratedTokens: r.GeneratedTokens, EnergyJ: r.EnergyJ}
	}
	rep := CompareReport{SchemaVersion: 1, A: side(ua), B: side(ub), Tokens: ua.GeneratedTokens}
	project(&rep)
	rep.Notes = append(rep.Notes, "traffic")
	return rep, nil
}

func (a *app) writeCompare(cmd *cobra.Command, rep CompareReport) error {
	l := a.l
	g := output.Grid{Header: []string{"", "A", "B"}}
	ci := func(s CompareSide) string {
		v := units.Energy(s.JPerToken, a.raw)
		if s.CI95 > 0 {
			v += " +- " + units.Energy(s.CI95, a.raw)
		}
		return v
	}
	g.Rows = [][]string{
		{l.T("cli.compare.what"), rep.A.Label, rep.B.Label},
		{l.T("cli.col.joules_per_token"), ci(rep.A), ci(rep.B)},
		{l.T("cli.compare.tokens"), strconv.FormatInt(rep.A.GeneratedTokens, 10), strconv.FormatInt(rep.B.GeneratedTokens, 10)},
	}
	if a.out() != output.Table {
		return output.Write(cmd.OutOrStdout(), a.out(), rep, g)
	}
	w := cmd.OutOrStdout()
	if err := output.WriteGrid(w, g); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(w)
	key := "cli.compare.saves"
	saved := rep.SavedJ
	if saved < 0 {
		key, saved = "cli.compare.costs", -saved
	}
	co2 := units.CO2e(math.Abs(rep.Saved.CO2eG), a.raw)
	_, _ = fmt.Fprintln(w, l.T(key, "tokens", strconv.FormatInt(rep.Tokens, 10), "a", units.Energy(rep.EnergyAJ, a.raw), "b", units.Energy(rep.EnergyBJ, a.raw),
		"saved", units.Energy(saved, a.raw), "percent", strconv.FormatFloat(math.Abs(rep.SavedPercent), 'f', 1, 64), "co2e", co2))
	if rep.WithinNoise {
		_, _ = fmt.Fprintln(w, a.paint(l.T("cli.compare.noise"), true))
	}
	for _, n := range rep.Notes {
		_, _ = fmt.Fprintln(w, "  - "+l.T("cli.compare.note."+n))
	}
	return nil
}
