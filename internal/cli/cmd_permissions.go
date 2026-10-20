// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
)

// The rules are the files shipped in deploy/udev; a test keeps them equal.
var (
	raplRule = "# Read access to the RAPL energy counters for the greenlens-power group, so\n" +
		"# the GreenLens agent can measure without running as root. The counters are\n" +
		"# readable only by root by default because of the RAPL side channel\n" +
		"# (CVE-2020-8694); granting them to a dedicated group keeps that exposure\n" +
		"# limited to its members.\n" +
		`ACTION=="add", SUBSYSTEM=="powercap", KERNEL=="intel-rapl:*|intel-rapl-mmio:*", RUN+="/bin/chgrp greenlens-power /sys%p/energy_uj", RUN+="/bin/chmod g+r /sys%p/energy_uj"` + "\n"
	ipmiRule = "# Access to the IPMI device for the greenlens-power group, so the GreenLens\n" +
		"# agent can read the server power from the BMC without running as root.\n" +
		`SUBSYSTEM=="ipmi", KERNEL=="ipmi[0-9]*", GROUP="greenlens-power", MODE="0660"` + "\n"
)

const (
	raplRulePath = "/etc/udev/rules.d/70-greenlens-power.rules"
	ipmiRulePath = "/etc/udev/rules.d/71-greenlens-ipmi.rules"
)

var userName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// step is one change of the plan: a file to write or a command to run.
type step struct {
	desc    string
	file    string
	content string
	argv    []string
}

type permissionOpts struct {
	apply, yes, perfmon, ipmi bool
	user                      string
}

// system performs the changes; tests replace it.
type system interface {
	euid() int
	groupExists(name string) bool
	writeFile(path, content string) error
	run(ctx context.Context, argv []string) error
}

type realSystem struct{}

func (realSystem) euid() int { return os.Geteuid() }
func (realSystem) groupExists(name string) bool {
	_, err := user.LookupGroup(name)
	return err == nil
}
func (realSystem) writeFile(p, c string) error { return os.WriteFile(p, []byte(c), 0o644) } //nolint:gosec // udev rules are world readable
func (realSystem) run(ctx context.Context, argv []string) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // fixed programs; arguments are validated
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", strings.Join(argv, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func newSetupPermissionsCmd(a *app) *cobra.Command {
	l := a.l
	var o permissionOpts
	cmd := &cobra.Command{
		Use:   "setup-permissions",
		Short: l.T("cli.perm.short"),
		Long:  wrap(l.T("cli.perm.long"), helpWidth),
		Example: "  greenlens setup-permissions\n  sudo greenlens setup-permissions --apply --user $USER\n" +
			"  sudo greenlens setup-permissions --apply --perfmon --ipmi",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sys := a.hooks.system
			if sys == nil {
				sys = realSystem{}
			}
			return a.setupPermissions(cmd, sys, o)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.apply, "apply", false, l.T("cli.perm.flag.apply"))
	f.BoolVar(&o.yes, "yes", false, l.T("cli.perm.flag.yes"))
	f.BoolVar(&o.perfmon, "perfmon", false, l.T("cli.perm.flag.perfmon"))
	f.BoolVar(&o.ipmi, "ipmi", false, l.T("cli.perm.flag.ipmi"))
	f.StringVar(&o.user, "user", "", l.T("cli.perm.flag.user"))
	return cmd
}

func (a *app) permissionPlan(sys system, o permissionOpts) ([]step, error) {
	l := a.l
	var plan []step
	if !sys.groupExists(PowerGroup) {
		plan = append(plan, step{desc: l.T("cli.perm.step.group", "group", PowerGroup), argv: []string{"groupadd", "--system", PowerGroup}})
	}
	plan = append(plan, step{desc: l.T("cli.perm.step.rule", "path", raplRulePath), file: raplRulePath, content: raplRule})
	trigger := []string{"udevadm", "trigger", "--action=add", "--subsystem-match=powercap"}
	if o.ipmi {
		plan = append(plan, step{desc: l.T("cli.perm.step.rule", "path", ipmiRulePath), file: ipmiRulePath, content: ipmiRule})
		trigger = append(trigger, "--subsystem-match=ipmi")
	}
	plan = append(plan,
		step{desc: l.T("cli.perm.step.reload"), argv: []string{"udevadm", "control", "--reload"}},
		step{desc: l.T("cli.perm.step.trigger"), argv: trigger},
	)
	if o.user != "" {
		if !userName.MatchString(o.user) {
			return nil, help.New(help.GL0005, "flag", "--user", "value", o.user, "command", "greenlens setup-permissions")
		}
		plan = append(plan, step{desc: l.T("cli.perm.step.user", "user", o.user, "group", PowerGroup), argv: []string{"usermod", "-aG", PowerGroup, o.user}})
	}
	if o.perfmon {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		plan = append(plan, step{desc: l.T("cli.perm.step.perfmon", "path", exe), argv: []string{"setcap", "cap_perfmon=ep", exe}})
	}
	return plan, nil
}

func (a *app) setupPermissions(cmd *cobra.Command, sys system, o permissionOpts) error {
	l := a.l
	plan, err := a.permissionPlan(sys, o)
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(w, l.T("cli.perm.plan"))
	for i, s := range plan {
		_, _ = fmt.Fprintf(w, "  %d. %s\n", i+1, s.desc)
		if s.file != "" {
			for _, line := range strings.Split(strings.TrimRight(s.content, "\n"), "\n") {
				_, _ = fmt.Fprintf(w, "       %s\n", line)
			}
		} else {
			_, _ = fmt.Fprintf(w, "       %s\n", strings.Join(s.argv, " "))
		}
	}
	_, _ = fmt.Fprintln(w, "\n"+wrap(l.T("cli.perm.risk"), helpWidth))
	if !o.apply {
		_, err := fmt.Fprintln(w, "\n"+l.T("cli.perm.how"))
		return err
	}
	if sys.euid() != 0 {
		name := "uid " + fmt.Sprint(sys.euid())
		if u, err := user.Current(); err == nil {
			name = u.Username
		}
		return help.New(help.GL1011, "user", name)
	}
	if !o.yes {
		_, _ = fmt.Fprint(w, "\n"+l.T("cli.perm.confirm")+" ")
		if !confirmed(cmd.InOrStdin()) {
			_, err := fmt.Fprintln(w, l.T("cli.perm.cancelled"))
			return err
		}
	}
	for i, s := range plan {
		if s.file != "" {
			err = sys.writeFile(s.file, s.content)
		} else {
			err = sys.run(cmd.Context(), s.argv)
		}
		if err != nil {
			return help.New(help.GL1012, "step", i+1, "detail", err.Error()).Wrap(err)
		}
		_, _ = fmt.Fprintf(w, "  %s %d. %s\n", a.paint(l.T("cli.perm.done"), false), i+1, s.desc)
	}
	_, err = fmt.Fprintln(w, "\n"+wrap(l.T("cli.perm.after"), helpWidth))
	return err
}

func confirmed(r io.Reader) bool {
	line, _ := bufio.NewReader(r).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "s", "si", "sí":
		return true
	}
	return false
}
