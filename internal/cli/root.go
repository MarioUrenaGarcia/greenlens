// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/i18n"
)

func newRoot(l *i18n.Localizer) *cobra.Command {
	root := &cobra.Command{
		Use:           "greenlens",
		Short:         l.T("cli.root.short"),
		Long:          wrap(l.T("cli.root.long"), helpWidth),
		SilenceErrors: true,
		SilenceUsage:  true,
		// Without Args, cobra reports unknown commands with its own English
		// message; this validator turns them into GL0002.
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownCommand(cmd, args[0])
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		// SuggestionsFor does not apply cobra's default distance, only the
		// internal suggestion path does.
		SuggestionsMinimumDistance: 2,
	}
	root.PersistentFlags().String("lang", "", l.T("cli.flag.lang"))
	root.SetFlagErrorFunc(flagError)
	root.SetUsageTemplate(usageTemplate(l))
	root.SetHelpCommand(newHelpCmd(l))
	root.AddCommand(newVersionCmd(l))
	localizeCommands(root, l)
	return root
}

// newHelpCmd replaces cobra's help command, whose texts are not translatable.
func newHelpCmd(l *i18n.Localizer) *cobra.Command {
	return &cobra.Command{
		Use:   "help [" + l.T("cli.usage.command_placeholder") + "]",
		Short: l.T("cli.help_cmd.short"),
		Long:  wrap(l.T("cli.help_cmd.long"), helpWidth),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := cmd.Root()
			target, rest, err := root.Find(args)
			if err != nil {
				return err
			}
			if len(rest) > 0 {
				return unknownCommand(target, rest[0])
			}
			return target.Help()
		},
	}
}

// localizeCommands defines the help flag of every command before cobra adds
// its own, which carries an English description, and stops cobra from
// appending its untranslated "[flags]" to the usage line; the usage template
// adds the translated placeholder instead.
func localizeCommands(cmd *cobra.Command, l *i18n.Localizer) {
	cmd.Flags().BoolP("help", "h", false, l.T("cli.flag.help", "command", cmd.Name()))
	cmd.DisableFlagsInUseLine = true
	for _, c := range cmd.Commands() {
		localizeCommands(c, l)
	}
}

// usageTemplate mirrors cobra's default usage template with translated
// headings. Headings are inserted as plain text, so a translation must not
// contain template delimiters; the catalogs never do.
func usageTemplate(l *i18n.Localizer) string {
	var b strings.Builder
	b.WriteString(l.T("cli.usage.usage"))
	b.WriteString("{{if .Runnable}}\n  {{.UseLine}}{{if .HasAvailableFlags}} [" + l.T("cli.usage.flags_placeholder") + "]{{end}}{{end}}")
	b.WriteString("{{if .HasAvailableSubCommands}}\n  {{.CommandPath}} [" + l.T("cli.usage.command_placeholder") + "]{{end}}")
	b.WriteString("{{if gt (len .Aliases) 0}}\n\n" + l.T("cli.usage.aliases") + "\n  {{.NameAndAliases}}{{end}}")
	b.WriteString("{{if .HasExample}}\n\n" + l.T("cli.usage.examples") + "\n{{.Example}}{{end}}")
	b.WriteString("{{if .HasAvailableSubCommands}}\n\n" + l.T("cli.usage.commands"))
	b.WriteString("{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name \"help\"))}}\n  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}")
	b.WriteString("{{if .HasAvailableLocalFlags}}\n\n" + l.T("cli.usage.flags") + "\n{{.LocalFlags.FlagUsagesWrapped 80 | trimTrailingWhitespaces}}{{end}}")
	b.WriteString("{{if .HasAvailableInheritedFlags}}\n\n" + l.T("cli.usage.global_flags") + "\n{{.InheritedFlags.FlagUsagesWrapped 80 | trimTrailingWhitespaces}}{{end}}")
	b.WriteString("{{if .HasAvailableSubCommands}}\n\n" + l.T("cli.usage.more", "command", "{{.CommandPath}}") + "{{end}}\n")
	return b.String()
}
