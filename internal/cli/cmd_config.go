// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/output"
)

func newConfigCmd(a *app) *cobra.Command {
	l := a.l
	cmd := &cobra.Command{
		Use:   "config",
		Short: l.T("cli.config.short"),
		Long:  wrap(l.T("cli.config.long"), helpWidth),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownCommand(cmd, args[0])
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:     "validate",
			Short:   l.T("cli.config.validate"),
			Example: "  greenlens config validate\n  greenlens config validate --config ./greenlens.yaml",
			Args:    noArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				_, path, err := a.config()
				if err != nil {
					return err
				}
				if path == "" {
					path = l.T("cli.config.defaults")
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), l.T("cli.config.valid", "path", path))
				return err
			},
		},
		&cobra.Command{
			Use:     "show",
			Short:   l.T("cli.config.show"),
			Example: "  greenlens config show\n  greenlens config show --output json",
			Args:    noArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				cfg, _, err := a.config()
				if err != nil {
					return err
				}
				if a.out() == output.Table || a.out() == output.YAML {
					b, err := yaml.Marshal(cfg)
					if err != nil {
						return err
					}
					_, err = cmd.OutOrStdout().Write(b)
					return err
				}
				return output.Write(cmd.OutOrStdout(), a.out(), cfg)
			},
		},
		&cobra.Command{
			Use:     "path",
			Short:   l.T("cli.config.path"),
			Example: "  greenlens config path\n  greenlens config path --output json",
			Args:    noArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				_, path, err := a.config()
				if err != nil {
					return err
				}
				doc := struct {
					SchemaVersion int      `json:"schema_version"`
					Path          string   `json:"path"`
					Searched      []string `json:"searched"`
				}{1, path, config.Paths(a.getenv)}
				if a.out() != output.Table {
					return output.Write(cmd.OutOrStdout(), a.out(), doc)
				}
				w := cmd.OutOrStdout()
				if path == "" {
					_, _ = fmt.Fprintln(w, l.T("cli.config.none"))
				} else {
					_, _ = fmt.Fprintln(w, path)
				}
				for _, p := range doc.Searched {
					_, _ = fmt.Fprintln(w, "  "+l.T("cli.config.searched", "path", p))
				}
				return nil
			},
		},
	)
	return cmd
}
