// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/auth"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/output"
)

// browserWait is how long xdg-open gets to fail. It may keep running as long
// as the browser it started, so a program still running then counts as
// opened.
const browserWait = 3 * time.Second

func newGUICmd(a *app) *cobra.Command {
	l := a.l
	var printOnly bool
	var role string
	minutes := strconv.Itoa(int(auth.CodeLifetime.Minutes()))
	cmd := &cobra.Command{
		Use:     "gui",
		Short:   l.T("cli.gui.short"),
		Long:    wrap(l.T("cli.gui.long", "minutes", minutes), helpWidth),
		Example: "  greenlens gui\n  greenlens gui --print --role viewer",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := a.runGUI(cmd, printOnly, role, minutes)
			if err != nil && a.fromDesktop() {
				// Started from the launcher, nobody reads the terminal.
				e := help.As(err)
				_ = a.notifier()("GreenLens", e.Message(l)+"\n"+e.Fix(l))
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&printOnly, "print", false, l.T("cli.gui.flag.print"))
	cmd.Flags().StringVar(&role, "role", string(auth.Admin), l.T("cli.gui.flag.role"))
	return cmd
}

func (a *app) runGUI(cmd *cobra.Command, printOnly bool, role, minutes string) error {
	l := a.l
	r, err := auth.ParseRole(role)
	if err != nil {
		return help.New(help.GL0005, "flag", "--role", "value", role, "command", cmd.CommandPath())
	}
	cfg, _, err := a.config()
	if err != nil {
		return err
	}
	c, err := a.agent(cmd.Context(), cfg)
	if err != nil {
		return err
	}
	code, err := c.SessionCode(cmd.Context(), string(r))
	if err != nil {
		return err
	}
	u, err := url.Parse(code.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("the agent returned a sign-in link that is not a web address: %q", code.URL)
	}
	w := cmd.OutOrStdout()
	if a.out() != output.Table {
		return output.Write(w, a.out(), code, output.Grid{Header: []string{"url", "expires_at"},
			Rows: [][]string{{code.URL, code.ExpiresAt.Format(time.RFC3339)}}})
	}
	if printOnly {
		_, err := fmt.Fprintln(w, code.URL)
		return err
	}
	remote := a.overSSH()
	if !remote && a.hasDisplay() && a.opener()(code.URL) == nil {
		_, _ = fmt.Fprintln(w, wrap(l.T("cli.gui.opening", "minutes", minutes), helpWidth))
		_, err := fmt.Fprintf(w, "\n  %s\n", code.URL)
		return err
	}
	if host := u.Hostname(); remote && isLoopbackHost(host) {
		port := u.Port()
		_, _ = fmt.Fprintln(w, wrap(l.T("cli.gui.tunnel"), helpWidth))
		_, _ = fmt.Fprintf(w, "\n  ssh -N -L %s:%s <%s>\n\n", port, net.JoinHostPort(host, port), l.T("cli.gui.server"))
		_, _ = fmt.Fprintf(w, "%s\n\n", wrap(l.T("cli.gui.tunnel_then"), helpWidth))
	}
	_, _ = fmt.Fprintln(w, wrap(l.T("cli.gui.link", "minutes", minutes), helpWidth))
	_, err = fmt.Fprintf(w, "\n  %s\n", code.URL)
	return err
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// overSSH reports whether the command runs in an SSH session, where a
// browser would open on the server instead of the computer of the user.
func (a *app) overSSH() bool {
	return a.getenv("SSH_CONNECTION") != "" || a.getenv("SSH_CLIENT") != "" || a.getenv("SSH_TTY") != ""
}

func (a *app) hasDisplay() bool {
	return a.getenv("WAYLAND_DISPLAY") != "" || a.getenv("DISPLAY") != ""
}

// fromDesktop reports whether the command was started by the desktop
// launcher: a graphical session and no terminal to show errors in.
func (a *app) fromDesktop() bool {
	return a.hasDisplay() && !a.overSSH() && !isTerminal(a.stderr)
}

func (a *app) opener() func(string) error {
	if a.hooks.open != nil {
		return a.hooks.open
	}
	return openBrowser
}

func (a *app) notifier() func(string, string) error {
	if a.hooks.notify != nil {
		return a.hooks.notify
	}
	return notifyDesktop
}

// openBrowser starts xdg-open, which fails at once when the desktop has no
// program for web links.
func openBrowser(link string) error {
	cmd := exec.Command("xdg-open", link) //nolint:gosec,noctx // fixed program; the link is a checked web address from the agent, and the browser must outlive this command
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(browserWait):
		return nil
	}
}

// notifyDesktop shows a notification with notify-send, when it exists.
func notifyDesktop(title, body string) error {
	path, err := exec.LookPath("notify-send")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--app-name=GreenLens", "--", title, body).CombinedOutput() //nolint:gosec // fixed program; the texts are passed as arguments
	if err != nil {
		return errors.Join(err, errors.New(string(out)))
	}
	return nil
}
