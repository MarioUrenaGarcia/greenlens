// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/hostfs"
	"github.com/MarioUrenaGarcia/greenlens/internal/output"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/estimate"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/registry"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/selector"
	"github.com/MarioUrenaGarcia/greenlens/internal/units"
)

// PowerGroup is the group that may read the energy counters.
const PowerGroup = "greenlens-power"

// Environment describes the machine as doctor sees it.
type Environment struct {
	Kernel         string `json:"kernel"`
	Virtualized    bool   `json:"virtualized"`
	Container      bool   `json:"container"`
	CgroupV2       bool   `json:"cgroup_v2"`
	PerfParanoid   string `json:"perf_event_paranoid"`
	PowerGroup     bool   `json:"power_group_member"`
	PowerGroupLive bool   `json:"power_group_active"`
	LandlockABI    int    `json:"landlock_abi"`
	CPUModel       string `json:"cpu_model"`
}

// DoctorReport is the document of greenlens doctor.
type DoctorReport struct {
	SchemaVersion int             `json:"schema_version"`
	Environment   Environment     `json:"environment"`
	Estimated     bool            `json:"estimated"`
	EstimateModel *estimate.Model `json:"estimate_model,omitempty"`
	Channels      []api.Channel   `json:"channels"`
	Problems      []api.Problem   `json:"problems,omitempty"`
	Config        string          `json:"config"`
	ConfigError   string          `json:"config_error,omitempty"`
	Agent         string          `json:"agent,omitempty"`
}

func newDoctorCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "doctor",
		Short:   a.l.T("cli.doctor.short"),
		Long:    wrap(a.l.T("cli.doctor.long"), helpWidth),
		Example: "  greenlens doctor\n  greenlens doctor --output json",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rep := a.diagnose(cmd.Context())
			return a.writeDoctor(cmd, rep)
		},
	}
}

func (a *app) diagnose(ctx context.Context) DoctorReport {
	rep := DoctorReport{SchemaVersion: api.SchemaVersion}
	cfg, path, err := a.config()
	rep.Config = path
	if err != nil {
		// A broken configuration is itself a finding; the rest of the
		// diagnosis runs with the defaults.
		rep.ConfigError = help.RenderAll(a.l, err)
		cfg = config.Defaults()
	}
	host := a.hostFS(cfg)
	rep.Environment = a.environment(host)

	srcs := a.sources(cfg)
	defer func() { _ = registry.Close(srcs) }()
	cands, perr := registry.Probe(ctx, srcs)
	if perr != nil {
		rep.ConfigError = strings.TrimSpace(rep.ConfigError + "\n" + perr.Error())
	}
	sel := selector.Select(cands, cfg.SelectorOptions())
	rep.Estimated = sel.Estimated
	for _, s := range srcs {
		if e, ok := s.(*estimate.Source); ok && rep.Estimated {
			m := e.Model
			rep.EstimateModel = &m
		}
	}
	for _, d := range sel.Decisions {
		ch := api.Channel{
			ID: d.Channel.ID, Source: d.Channel.Source, Domain: d.Channel.Domain, Instance: d.Channel.Instance,
			Quality: d.Channel.Quality, Method: d.Channel.Method, Label: d.Channel.Label, Selected: d.Selected, Reason: d.Reason.Key,
		}
		for i := 0; i+1 < len(d.Reason.Args); i += 2 {
			if k, ok := d.Reason.Args[i].(string); ok {
				if ch.ReasonArgs == nil {
					ch.ReasonArgs = map[string]string{}
				}
				ch.ReasonArgs[k] = fmt.Sprint(d.Reason.Args[i+1])
			}
		}
		rep.Channels = append(rep.Channels, ch)
	}
	for _, p := range sel.Problems {
		rep.Problems = append(rep.Problems, api.FromProblem(p))
	}
	actx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if c, err := a.agent(actx, cfg); err == nil {
		rep.Agent = c.Socket
	}
	return rep
}

func (a *app) environment(host fs.FS) Environment {
	var env Environment
	env.Kernel, _ = hostfs.String(host, "proc/sys/kernel/osrelease")
	if cpuinfo, err := fs.ReadFile(host, "proc/cpuinfo"); err == nil {
		env.Virtualized = strings.Contains(string(cpuinfo), " hypervisor")
	}
	if c, err := estimate.ReadCPU(host); err == nil {
		env.CPUModel = c.ModelName
	}
	env.Container = hostfs.Exists(a.selfFS(), ".dockerenv") || hostfs.Exists(a.selfFS(), "run/.containerenv") || a.getenv("container") != ""
	env.CgroupV2 = hostfs.Exists(host, "sys/fs/cgroup/cgroup.controllers")
	env.PerfParanoid, _ = hostfs.String(host, "proc/sys/kernel/perf_event_paranoid")
	env.PowerGroup, env.PowerGroupLive = groupMembership(PowerGroup)
	env.LandlockABI = landlockABI()
	return env
}

// groupMembership reports whether the user belongs to group, and whether
// this process already has it; membership added after login only applies
// to new sessions.
func groupMembership(group string) (member, active bool) {
	g, err := user.LookupGroup(group)
	if err != nil {
		return false, false
	}
	if u, err := user.Current(); err == nil {
		if ids, err := u.GroupIds(); err == nil {
			member = slices.Contains(ids, g.Gid)
		}
	}
	if gids, err := os.Getgroups(); err == nil {
		for _, id := range gids {
			if strconv.Itoa(id) == g.Gid {
				active = true
			}
		}
	}
	return member, active
}

func (a *app) writeDoctor(cmd *cobra.Command, rep DoctorReport) error {
	l := a.l
	g := output.Grid{Header: []string{l.T("cli.col.source"), l.T("cli.col.channel"), l.T("cli.col.domain"), l.T("cli.col.quality"), l.T("cli.col.status"), l.T("cli.col.reason")}}
	for _, c := range rep.Channels {
		status := a.paint(l.T("cli.doctor.selected"), false)
		if !c.Selected {
			status = l.T("cli.doctor.discarded")
		}
		var args []any
		for k, v := range c.ReasonArgs {
			args = append(args, k, v)
		}
		g.Rows = append(g.Rows, []string{c.Source, c.ID, a.domainName(c.Domain), a.qualityName(c.Quality), status, l.T(c.Reason, args...)})
	}
	if a.out() != output.Table {
		return output.Write(cmd.OutOrStdout(), a.out(), rep, g)
	}
	w := cmd.OutOrStdout()
	e := rep.Environment
	yes := func(b bool) string {
		if b {
			return l.T("cli.yes")
		}
		return l.T("cli.no")
	}
	env := output.Grid{Rows: [][]string{
		{l.T("cli.doctor.kernel"), e.Kernel},
		{l.T("cli.doctor.cpu"), e.CPUModel},
		{l.T("cli.doctor.virtualized"), yes(e.Virtualized)},
		{l.T("cli.doctor.container"), yes(e.Container)},
		{l.T("cli.doctor.cgroup"), yes(e.CgroupV2)},
		{l.T("cli.doctor.paranoid"), e.PerfParanoid},
		{l.T("cli.doctor.group", "group", PowerGroup), groupState(l.T, e)},
		{l.T("cli.doctor.landlock"), landlockState(l.T, e.LandlockABI)},
	}}
	cfgPath := rep.Config
	if cfgPath == "" {
		cfgPath = l.T("cli.config.defaults")
	}
	env.Rows = append(env.Rows, []string{l.T("cli.doctor.config"), cfgPath})
	agentState := a.paint(l.T("cli.doctor.agent_off"), true)
	if rep.Agent != "" {
		agentState = a.paint(l.T("cli.doctor.agent_on", "socket", rep.Agent), false)
	}
	env.Rows = append(env.Rows, []string{l.T("cli.doctor.agent"), agentState})
	_, _ = fmt.Fprintln(w, l.T("cli.doctor.environment"))
	if err := output.WriteGrid(w, env); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(w, "\n"+l.T("cli.doctor.sources"))
	if err := output.WriteGrid(w, g); err != nil {
		return err
	}
	if rep.Estimated && rep.EstimateModel != nil {
		_, _ = fmt.Fprintln(w, "\n"+a.paint(l.T("cli.doctor.estimated", "model", a.modelText(*rep.EstimateModel)), true))
	} else {
		_, _ = fmt.Fprintln(w, "\n"+a.paint(l.T("cli.doctor.measured"), false))
	}
	if rep.ConfigError != "" {
		_, _ = fmt.Fprintln(w, "\n"+rep.ConfigError)
	}
	return a.writeProblems(w, rep.Problems)
}

func groupState(t func(string, ...any) string, e Environment) string {
	switch {
	case e.PowerGroupLive:
		return t("cli.doctor.group_active")
	case e.PowerGroup:
		return t("cli.doctor.group_relogin")
	default:
		return t("cli.doctor.group_missing")
	}
}

func landlockState(t func(string, ...any) string, abi int) string {
	if abi <= 0 {
		return t("cli.doctor.landlock_off")
	}
	return t("cli.doctor.landlock_on", "abi", abi)
}

func (a *app) modelText(m estimate.Model) string {
	if m.Configured {
		return a.l.T("cli.doctor.model_configured", "idle", units.Power(m.IdleW, a.raw), "full", units.Power(m.FullW, a.raw))
	}
	name := m.Name
	if name == estimate.Generic.Name {
		name = a.l.T("cli.doctor.model_generic")
	}
	return a.l.T("cli.doctor.model", "name", name, "threads", m.Threads,
		"min", fmt.Sprintf("%.2f", m.MinW), "max", fmt.Sprintf("%.2f", m.MaxW), "idle", units.Power(m.IdleW, a.raw), "full", units.Power(m.FullW, a.raw))
}
