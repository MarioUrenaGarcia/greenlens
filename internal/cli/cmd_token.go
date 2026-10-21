// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/agent"
	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/auth"
	"github.com/MarioUrenaGarcia/greenlens/internal/client"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/output"
	"github.com/MarioUrenaGarcia/greenlens/internal/paths"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

// tokenStore manages tokens through the running agent, which records the
// change in its audit log, or directly in the token file when no agent
// runs, as before the first start of an agent that listens on the network.
type tokenStore struct {
	c    *client.Client
	file *auth.Tokens
	db   string
}

func (a *app) tokenStore(ctx context.Context, cfg config.Config) (tokenStore, error) {
	if c, ok := a.runningAgent(ctx, cfg); ok {
		return tokenStore{c: c}, nil
	}
	dir, err := paths.DataDir(cfg, a.getenv)
	if err != nil {
		return tokenStore{}, help.New(help.GL4002, "path", "agent.data_dir", "detail", err.Error()).Wrap(err)
	}
	p := cfg.Server.TokensFile
	if p == "" {
		p = filepath.Join(dir, agent.TokensName)
	}
	f, err := auth.OpenTokens(p)
	if err != nil {
		return tokenStore{}, help.New(help.GL4013, "path", p, "detail", err.Error()).Wrap(err)
	}
	return tokenStore{file: f, db: filepath.Join(dir, paths.DatabaseName)}, nil
}

// audit records a change made without the agent in its database, when the
// database exists.
func (s tokenStore) audit(ctx context.Context, a *app, action, detail string) {
	if !fileExists(s.db) {
		return
	}
	st, err := store.Open(ctx, s.db)
	if err != nil {
		a.say(err.Error())
		return
	}
	defer func() { _ = st.Close() }()
	if err := st.Audit(ctx, "uid:"+strconv.Itoa(os.Getuid()), action, detail); err != nil {
		a.say(err.Error())
	}
}

func newTokenCmd(a *app) *cobra.Command {
	l := a.l
	cmd := &cobra.Command{
		Use:   "token",
		Short: l.T("cli.token.short"),
		Long:  wrap(l.T("cli.token.long"), helpWidth),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownCommand(cmd, args[0])
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newTokenCreateCmd(a), newTokenListCmd(a), newTokenRevokeCmd(a))
	return cmd
}

// parseLifetime accepts "never", amounts such as 90d or 12w, and Go
// durations such as 36h.
func parseLifetime(s string) (time.Duration, error) {
	if s == "never" || s == "0" {
		return 0, nil
	}
	if m := relative.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil || n <= 0 {
			return 0, errors.New("must be positive")
		}
		unit := map[string]time.Duration{"m": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}[m[2]]
		return time.Duration(n) * unit, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, errors.New("must be a positive duration")
	}
	return d, nil
}

func newTokenCreateCmd(a *app) *cobra.Command {
	l := a.l
	var name, role, expires string
	cmd := &cobra.Command{
		Use:   "create",
		Short: l.T("cli.token.create.short"),
		Long:  wrap(l.T("cli.token.create.long"), helpWidth),
		Example: "  greenlens token create --name grafana --role viewer --expires 90d\n" +
			"  sudo -u greenlens greenlens token create --name admin --role admin",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := auth.ParseRole(role)
			if err != nil {
				return help.New(help.GL0005, "flag", "--role", "value", role, "command", cmd.CommandPath())
			}
			ttl, err := parseLifetime(expires)
			if err != nil || ttl > 10*366*24*time.Hour {
				return help.New(help.GL0005, "flag", "--expires", "value", expires, "command", cmd.CommandPath())
			}
			cfg, _, err := a.config()
			if err != nil {
				return err
			}
			s, err := a.tokenStore(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			var out api.CreatedToken
			if s.c != nil {
				if out, err = s.c.CreateToken(cmd.Context(), api.CreateToken{Name: name, Role: string(r), ExpiresInS: int64(ttl.Seconds())}); err != nil {
					return err
				}
			} else {
				secret, tk, err := s.file.Create(name, r, ttl)
				if err != nil {
					return help.New(help.GL0005, "flag", "--name", "value", name, "command", cmd.CommandPath()).Wrap(err)
				}
				out = api.CreatedToken{SchemaVersion: api.SchemaVersion, Token: tokenDoc(tk), Secret: secret}
				s.audit(cmd.Context(), a, "token_create", fmt.Sprintf("%s (%s), role %s", tk.Name, tk.ID, tk.Role))
			}
			if a.out() != output.Table {
				return output.Write(cmd.OutOrStdout(), a.out(), out, output.Grid{Header: []string{"id", "name", "role", "secret"},
					Rows: [][]string{{out.Token.ID, out.Token.Name, out.Token.Role, out.Secret}}})
			}
			w := cmd.OutOrStdout()
			_, _ = fmt.Fprintln(w, wrap(l.T("cli.token.created", "name", out.Token.Name, "role", out.Token.Role), helpWidth))
			_, _ = fmt.Fprintf(w, "\n  %s\n\n", out.Secret)
			_, err = fmt.Fprintln(w, wrap(l.T("cli.token.use"), helpWidth))
			return err
		},
	}
	cmd.Flags().StringVar(&name, "name", "", l.T("cli.token.flag.name"))
	cmd.Flags().StringVar(&role, "role", string(auth.Viewer), l.T("cli.token.flag.role"))
	cmd.Flags().StringVar(&expires, "expires", "never", l.T("cli.token.flag.expires"))
	return cmd
}

func tokenDoc(tk auth.Token) api.Token {
	return api.Token{ID: tk.ID, Name: tk.Name, Role: string(tk.Role), CreatedAt: tk.CreatedAt, ExpiresAt: tk.ExpiresAt}
}

func newTokenListCmd(a *app) *cobra.Command {
	l := a.l
	return &cobra.Command{
		Use:     "list",
		Short:   l.T("cli.token.list.short"),
		Long:    wrap(l.T("cli.token.list.long"), helpWidth),
		Example: "  greenlens token list\n  greenlens token list --output json",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := a.config()
			if err != nil {
				return err
			}
			s, err := a.tokenStore(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			doc := api.Tokens{SchemaVersion: api.SchemaVersion, Tokens: []api.Token{}}
			if s.c != nil {
				if doc, err = s.c.Tokens(cmd.Context()); err != nil {
					return err
				}
			} else {
				for _, tk := range s.file.List() {
					doc.Tokens = append(doc.Tokens, tokenDoc(tk))
				}
			}
			g := output.Grid{Header: []string{"ID", l.T("cli.col.name"), l.T("cli.col.role"), l.T("cli.col.created"), l.T("cli.col.expires")}}
			for _, tk := range doc.Tokens {
				exp := l.T("cli.token.never")
				if tk.ExpiresAt != nil {
					exp = tk.ExpiresAt.Local().Format(time.DateTime)
				}
				g.Rows = append(g.Rows, []string{tk.ID, tk.Name, tk.Role, tk.CreatedAt.Local().Format(time.DateTime), exp})
			}
			if a.out() != output.Table {
				return output.Write(cmd.OutOrStdout(), a.out(), doc, g)
			}
			if len(doc.Tokens) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), wrap(l.T("cli.token.none"), helpWidth))
				return err
			}
			return output.WriteGrid(cmd.OutOrStdout(), g)
		},
	}
}

func newTokenRevokeCmd(a *app) *cobra.Command {
	l := a.l
	return &cobra.Command{
		Use:     "revoke <" + l.T("cli.token.placeholder") + ">",
		Short:   l.T("cli.token.revoke.short"),
		Long:    wrap(l.T("cli.token.revoke.long"), helpWidth),
		Example: "  greenlens token revoke grafana\n  greenlens token revoke 3f9a1c0b7d2e",
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := a.config()
			if err != nil {
				return err
			}
			s, err := a.tokenStore(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			var tk api.Token
			if s.c != nil {
				list, err := s.c.Tokens(cmd.Context())
				if err != nil {
					return err
				}
				if !hasToken(list.Tokens, args[0]) {
					return help.New(help.GL0018, "token", args[0])
				}
				if tk, err = s.c.RevokeToken(cmd.Context(), args[0]); err != nil {
					return err
				}
			} else {
				t, err := s.file.Revoke(args[0])
				if errors.Is(err, auth.ErrNotFound) {
					return help.New(help.GL0018, "token", args[0])
				}
				if err != nil {
					return help.New(help.GL4013, "path", s.file.Path(), "detail", err.Error()).Wrap(err)
				}
				tk = tokenDoc(t)
				s.audit(cmd.Context(), a, "token_revoke", fmt.Sprintf("%s (%s)", t.Name, t.ID))
			}
			if a.out() != output.Table {
				return output.Write(cmd.OutOrStdout(), a.out(), tk, output.Grid{Header: []string{"id", "name", "role"}, Rows: [][]string{{tk.ID, tk.Name, tk.Role}}})
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), l.T("cli.token.revoked", "name", tk.Name, "id", tk.ID))
			return err
		},
	}
}

func hasToken(list []api.Token, idOrName string) bool {
	for _, tk := range list {
		if tk.ID == idOrName || tk.Name == idOrName {
			return true
		}
	}
	return false
}
