// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
)

// Topics are the concepts greenlens help explains.
var Topics = []string{"measurement", "attribution", "sources", "permissions", "privacy", "configuration"}

// Metrics are the values greenlens explain describes.
var Metrics = []string{
	"node_power", "node_energy", "idle_power", "dynamic_energy", "attributed_energy",
	"joules_per_token", "tokens_per_second", "token_cost", "co2e", "cost", "water", "quality", "method",
}

func isTopic(s string) bool {
	for _, t := range Topics {
		if t == s {
			return true
		}
	}
	return false
}

// writeTopic prints a help topic, one wrapped paragraph after another.
func (a *app) writeTopic(cmd *cobra.Command, topic string) error {
	l := a.l
	var b strings.Builder
	b.WriteString(l.T("topic."+topic+".title") + "\n\n")
	for _, p := range strings.Split(l.T("topic."+topic+".body"), "\n\n") {
		b.WriteString(wrap(p, helpWidth) + "\n\n")
	}
	b.WriteString(wrap(l.T("cli.topics.related", "commands", l.T("topic."+topic+".see")), helpWidth) + "\n")
	_, err := fmt.Fprint(cmd.OutOrStdout(), b.String())
	return err
}

// writeTopics lists the help topics.
func (a *app) writeTopics(cmd *cobra.Command) error {
	var b strings.Builder
	b.WriteString("\n" + a.l.T("cli.topics.heading") + "\n")
	for _, t := range Topics {
		fmt.Fprintf(&b, "  %-14s %s\n", t, a.l.T("topic."+t+".title"))
	}
	b.WriteString("\n" + wrap(a.l.T("cli.topics.more"), helpWidth) + "\n")
	_, err := fmt.Fprint(cmd.OutOrStdout(), b.String())
	return err
}

func newExplainCmd(a *app) *cobra.Command {
	l := a.l
	return &cobra.Command{
		Use:     "explain [" + l.T("cli.explain.placeholder") + "]",
		Short:   l.T("cli.explain.short"),
		Long:    wrap(l.T("cli.explain.long"), helpWidth),
		Example: "  greenlens explain\n  greenlens explain joules_per_token",
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			switch len(args) {
			case 0:
				var b strings.Builder
				b.WriteString(l.T("cli.explain.heading") + "\n")
				for _, m := range Metrics {
					fmt.Fprintf(&b, "  %-18s %s\n", m, l.T("explain."+m+".title"))
				}
				b.WriteString("\n" + wrap(l.T("cli.explain.more"), helpWidth) + "\n")
				_, err := fmt.Fprint(w, b.String())
				return err
			case 1:
			default:
				return help.New(help.GL0015, "command", cmd.CommandPath(), "want", 1, "got", len(args))
			}
			name := strings.ToLower(args[0])
			known := false
			for _, m := range Metrics {
				known = known || m == name
			}
			if !known {
				return help.New(help.GL0017, "metric", args[0], "metrics", strings.Join(Metrics, ", "))
			}
			var b strings.Builder
			b.WriteString(l.T("explain."+name+".title") + "\n\n")
			b.WriteString(wrap(l.T("explain."+name+".what"), helpWidth) + "\n\n")
			for _, part := range []string{"formula", "unit", "method", "limits"} {
				b.WriteString(l.T("cli.explain."+part) + "\n")
				b.WriteString(indent(wrap(l.T("explain."+name+"."+part), helpWidth-2), "  ") + "\n\n")
			}
			_, err := fmt.Fprint(w, strings.TrimRight(b.String(), "\n")+"\n")
			return err
		},
	}
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func newCompletionCmd(a *app) *cobra.Command {
	l := a.l
	return &cobra.Command{
		Use:       "completion bash|zsh|fish",
		Short:     l.T("cli.completion.short"),
		Long:      wrap(l.T("cli.completion.long"), helpWidth),
		Example:   "  greenlens completion bash > ~/.local/share/bash-completion/completions/greenlens\n  greenlens completion zsh > \"${fpath[1]}/_greenlens\"\n  greenlens completion fish > ~/.config/fish/completions/greenlens.fish",
		Args:      exactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish"},
		RunE: func(cmd *cobra.Command, args []string) error {
			root, w := cmd.Root(), cmd.OutOrStdout()
			switch args[0] {
			case "bash":
				return root.GenBashCompletionV2(w, true)
			case "zsh":
				return root.GenZshCompletion(w)
			case "fish":
				return root.GenFishCompletion(w, true)
			}
			return help.New(help.GL0005, "flag", "<shell>", "value", args[0], "command", cmd.CommandPath())
		},
	}
}
