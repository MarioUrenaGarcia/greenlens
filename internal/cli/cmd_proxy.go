// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/output"
)

func newProxyCmd(a *app) *cobra.Command {
	l := a.l
	return &cobra.Command{
		Use:     "proxy",
		Short:   l.T("cli.proxy.short"),
		Long:    wrap(l.T("cli.proxy.long"), helpWidth),
		Example: "  greenlens proxy\n  greenlens report --from 24h --group consumer",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := a.config()
			if err != nil {
				return err
			}
			c, err := a.agent(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			st, err := c.Status(cmd.Context())
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			p := st.Proxy
			if a.out() != output.Table {
				g := output.Grid{Header: []string{"listen", "upstream", "backend", "requests"}}
				if p != nil {
					g.Rows = [][]string{{p.Listen, p.Upstream, p.Backend, strconv.FormatUint(p.Requests, 10)}}
				}
				return output.Write(w, a.out(), p, g)
			}
			if p == nil {
				_, err := fmt.Fprintln(w, l.T("cli.proxy.disabled"))
				return err
			}
			yes, no := l.T("cli.proxy.yes"), l.T("cli.proxy.no")
			header := no
			if p.EnergyHeader {
				header = yes
			}
			consumers := no
			if p.ConsumerHeader != "" {
				consumers = p.ConsumerHeader
			}
			g := output.Grid{Rows: [][]string{
				{l.T("cli.proxy.listen"), p.Listen},
				{l.T("cli.proxy.upstream"), p.Upstream},
				{l.T("cli.proxy.backend"), p.Backend},
				{l.T("cli.proxy.requests"), strconv.FormatUint(p.Requests, 10)},
				{l.T("cli.proxy.energy_header"), header},
				{l.T("cli.proxy.consumers"), consumers},
			}}
			return output.WriteGrid(w, g)
		},
	}
}
