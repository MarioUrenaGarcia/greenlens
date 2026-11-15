// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/agent"
	"github.com/MarioUrenaGarcia/greenlens/internal/auth"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/hub"
	"github.com/MarioUrenaGarcia/greenlens/internal/output"
	"github.com/MarioUrenaGarcia/greenlens/internal/paths"
	"github.com/MarioUrenaGarcia/greenlens/internal/pki"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
	"github.com/MarioUrenaGarcia/greenlens/internal/version"
)

func newHubCmd(a *app) *cobra.Command {
	l := a.l
	cmd := &cobra.Command{
		Use:   "hub",
		Short: l.T("cli.hub.short"),
		Long:  wrap(l.T("cli.hub.long"), helpWidth),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownCommand(cmd, args[0])
			}
			return nil
		},
		Example: "  greenlens hub\n  greenlens hub --config /etc/greenlens/hub.yaml",
		RunE:    func(cmd *cobra.Command, _ []string) error { return a.runHub(cmd) },
	}
	cmd.AddCommand(newHubInitCACmd(a), newHubIssueCertCmd(a))
	return cmd
}

// hubDatabaseName is the database of the hub in the data directory. It is
// not the one of the agent, so both can run on the same machine.
const hubDatabaseName = "hub.db"

// runHub reads the agents listed in hub.nodes until it is interrupted.
func (a *app) runHub(cmd *cobra.Command) error {
	cfg, _, err := a.config()
	if err != nil {
		return err
	}
	if len(cfg.Hub.Nodes) == 0 {
		return help.New(help.GL3003, "key", "hub.nodes", "detail", "list the agents to read, and set hub.tls; 'greenlens hub --help' shows the commands that create the certificates")
	}
	dir, err := paths.DataDir(cfg, a.getenv)
	if err != nil {
		return help.New(help.GL4002, "path", "agent.data_dir", "detail", err.Error()).Wrap(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return help.New(help.GL4002, "path", dir, "detail", err.Error()).Wrap(err)
	}
	dbPath := filepath.Join(dir, hubDatabaseName)
	st, err := store.Open(cmd.Context(), dbPath)
	if err != nil {
		return help.New(help.GL4002, "path", dbPath, "detail", err.Error()).Wrap(err)
	}
	defer func() { _ = st.Close() }()
	tokensPath := cfg.Server.TokensFile
	if tokensPath == "" {
		tokensPath = filepath.Join(dir, agent.TokensName)
	}
	tokens, err := auth.OpenTokens(tokensPath)
	if err != nil {
		return help.New(help.GL4013, "path", tokensPath, "detail", err.Error()).Wrap(err)
	}
	level := new(slog.LevelVar)
	var lv slog.Level
	if err := lv.UnmarshalText([]byte(cfg.Logging.Level)); err == nil {
		level.Set(lv)
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler = slog.NewJSONHandler(a.stderr, opts)
	if cfg.Logging.Format == "text" {
		h = slog.NewTextHandler(a.stderr, opts)
	}
	err = hub.Run(cmd.Context(), hub.Options{
		Config: cfg.Hub, Store: st, Tokens: tokens, Logger: slog.New(h),
		Version: version.Get().Version, Retention: cfg.Retention.Rollup1h.D(),
	})
	if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, context.Canceled) {
		return help.New(help.GL4017, "detail", err.Error()).Wrap(err)
	}
	return nil
}

// certDoc is the machine-readable result of the certificate commands.
type certDoc struct {
	Name      string    `json:"name"`
	Role      string    `json:"role,omitempty"`
	CertFile  string    `json:"cert_file"`
	KeyFile   string    `json:"key_file"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
	Serial    string    `json:"serial"`
	Replaced  bool      `json:"replaced"`
}

func (d certDoc) grid() output.Grid {
	return output.Grid{Header: []string{"name", "role", "cert_file", "key_file", "not_after", "serial"},
		Rows: [][]string{{d.Name, d.Role, d.CertFile, d.KeyFile, d.NotAfter.UTC().Format(time.RFC3339), d.Serial}}}
}

func newHubInitCACmd(a *app) *cobra.Command {
	l := a.l
	var dir, name string
	var days int
	cmd := &cobra.Command{
		Use:   "init-ca",
		Short: l.T("cli.hub.init_ca.short"),
		Long:  wrap(l.T("cli.hub.init_ca.long"), helpWidth),
		Example: "  greenlens hub init-ca --dir /etc/greenlens/fleet-ca\n" +
			"  greenlens hub init-ca --dir ./fleet-ca --name inference-cluster --days 730",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dir == "" {
				return help.New(help.GL0005, "flag", "--dir", "value", dir, "command", cmd.CommandPath())
			}
			if days < 1 || days > pki.MaxCADays {
				return help.New(help.GL0005, "flag", "--days", "value", strconv.Itoa(days), "command", cmd.CommandPath())
			}
			abs, err := filepath.Abs(dir)
			if err != nil {
				return help.New(help.GL4016, "detail", err.Error(), "command", cmd.CommandPath()).Wrap(err)
			}
			ca, err := pki.InitCA(abs, name, days, time.Now())
			if err != nil {
				return help.New(help.GL4016, "detail", err.Error(), "command", cmd.CommandPath()).Wrap(err)
			}
			doc := certDoc{Name: name, CertFile: filepath.Join(abs, pki.CACertFile), KeyFile: filepath.Join(abs, pki.CAKeyFile),
				NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, Serial: ca.SerialNumber.Text(16)}
			if a.out() != output.Table {
				return output.Write(cmd.OutOrStdout(), a.out(), doc, doc.grid())
			}
			w := cmd.OutOrStdout()
			_, _ = fmt.Fprintln(w, wrap(l.T("cli.hub.ca_created", "name", name, "dir", abs, "date", ca.NotAfter.Local().Format(time.DateOnly)), helpWidth))
			_, err = fmt.Fprintln(w, wrap(l.T("cli.hub.ca_keep", "key", doc.KeyFile, "cert", doc.CertFile), helpWidth))
			return err
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", l.T("cli.hub.flag.dir"))
	cmd.Flags().StringVar(&name, "name", "greenlens-fleet", l.T("cli.hub.flag.ca_name"))
	cmd.Flags().IntVar(&days, "days", pki.DefaultCADays, l.T("cli.hub.flag.ca_days"))
	return cmd
}

func newHubIssueCertCmd(a *app) *cobra.Command {
	l := a.l
	var caDir, out, name, role string
	var hosts []string
	var days int
	cmd := &cobra.Command{
		Use:   "issue-cert",
		Short: l.T("cli.hub.issue_cert.short"),
		Long:  wrap(l.T("cli.hub.issue_cert.long"), helpWidth),
		Example: "  greenlens hub issue-cert --ca-dir ./fleet-ca --name node-a --host 10.0.0.11 --out ./certs\n" +
			"  greenlens hub issue-cert --ca-dir ./fleet-ca --name hub --role hub --days 14 --out ./certs",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			bad := func(flag, value string) error {
				return help.New(help.GL0005, "flag", flag, "value", value, "command", cmd.CommandPath())
			}
			switch {
			case caDir == "":
				return bad("--ca-dir", caDir)
			case out == "":
				return bad("--out", out)
			case !pki.ValidName(name):
				return bad("--name", name)
			case !slices.Contains(pki.Roles, role):
				return bad("--role", role)
			case days < 1 || days > pki.MaxCertDays:
				return bad("--days", strconv.Itoa(days))
			}
			absOut, err := filepath.Abs(out)
			if err != nil {
				return help.New(help.GL4016, "detail", err.Error(), "command", cmd.CommandPath()).Wrap(err)
			}
			is, err := pki.Issue(caDir, absOut, pki.Request{Name: name, Role: role, Hosts: hosts, Days: days}, time.Now())
			if err != nil {
				return help.New(help.GL4016, "detail", err.Error(), "command", cmd.CommandPath()).Wrap(err)
			}
			doc := certDoc{Name: name, Role: role, CertFile: is.CertPath, KeyFile: is.KeyPath, NotBefore: is.Cert.NotBefore,
				NotAfter: is.Cert.NotAfter, Serial: is.Cert.SerialNumber.Text(16), Replaced: is.Replaced}
			if a.out() != output.Table {
				return output.Write(cmd.OutOrStdout(), a.out(), doc, doc.grid())
			}
			w := cmd.OutOrStdout()
			_, _ = fmt.Fprintln(w, wrap(l.T("cli.hub.issued", "name", name, "role", role, "cert", is.CertPath, "key", is.KeyPath,
				"date", is.Cert.NotAfter.Local().Format(time.DateOnly)), helpWidth))
			if is.Replaced && role == pki.RoleAgent {
				_, _ = fmt.Fprintln(w, wrap(l.T("cli.hub.renewed", "name", name), helpWidth))
			}
			next := "cli.hub.next_agent"
			if role == pki.RoleHub {
				next = "cli.hub.next_hub"
			}
			_, err = fmt.Fprintln(w, wrap(l.T(next), helpWidth))
			return err
		},
	}
	cmd.Flags().StringVar(&caDir, "ca-dir", "", l.T("cli.hub.flag.ca_dir"))
	cmd.Flags().StringVar(&out, "out", "", l.T("cli.hub.flag.out"))
	cmd.Flags().StringVar(&name, "name", "", l.T("cli.hub.flag.name"))
	cmd.Flags().StringVar(&role, "role", pki.RoleAgent, l.T("cli.hub.flag.role"))
	cmd.Flags().StringArrayVar(&hosts, "host", nil, l.T("cli.hub.flag.host"))
	cmd.Flags().IntVar(&days, "days", pki.DefaultCertDays, l.T("cli.hub.flag.days"))
	return cmd
}
