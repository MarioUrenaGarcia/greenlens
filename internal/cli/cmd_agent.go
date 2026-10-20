// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/agent"
	"github.com/MarioUrenaGarcia/greenlens/internal/version"
)

func newAgentCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "agent",
		Short:   a.l.T("cli.agent.short"),
		Long:    wrap(a.l.T("cli.agent.long"), helpWidth),
		Example: "  greenlens agent\n  greenlens agent --config /etc/greenlens/greenlens.yaml",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, path, err := a.config()
			if err != nil {
				return err
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
			return agent.Run(cmd.Context(), agent.Options{
				Config: cfg, ConfigPath: path, Getenv: a.getenv, Logger: slog.New(h), Level: level,
				Version: version.Get().Version, Sources: a.hookSources(cfg), HostFS: a.hooks.hostFS,
			})
		},
	}
}
