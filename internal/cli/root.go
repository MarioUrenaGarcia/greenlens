// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/i18n"
)

func newRoot(a *app) *cobra.Command {
	l := a.l
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
		PersistentPreRunE: func(*cobra.Command, []string) error {
			return a.checkGlobalFlags()
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.brief(cmd.Context(), cmd)
		},
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		// SuggestionsFor does not apply cobra's default distance, only the
		// internal suggestion path does.
		SuggestionsMinimumDistance: 2,
	}
	root.PersistentFlags().String("lang", "", l.T("cli.flag.lang"))
	root.SetFlagErrorFunc(flagError)
	root.SetUsageTemplate(usageTemplate(l))
	a.addGlobalFlags(root)
	root.SetHelpCommand(newHelpCmd(a))
	root.AddCommand(
		newAgentCmd(a),
		newBackupCmd(a),
		newBenchCmd(a),
		newCalibrateCmd(a),
		newCompareCmd(a),
		newCompletionCmd(a),
		newConfigCmd(a),
		newDoctorCmd(a),
		newExplainCmd(a),
		newExportCmd(a),
		newGUICmd(a),
		newLicenseCmd(a),
		newMeasureCmd(a),
		newProxyCmd(a),
		newReportCmd(a),
		newRestoreCmd(a),
		newSetupPermissionsCmd(a),
		newStatusCmd(a),
		newTokenCmd(a),
		newTopCmd(a),
		newVersionCmd(l),
	)
	localizeCommands(root, l)
	return root
}

// newHelpCmd replaces cobra's help command, whose texts are not translatable.
// Besides commands, it explains error codes such as GL1002.
func newHelpCmd(a *app) *cobra.Command {
	l := a.l
	return &cobra.Command{
		Use:     "help [" + l.T("cli.usage.command_placeholder") + "]",
		Short:   l.T("cli.help_cmd.short"),
		Long:    wrap(l.T("cli.help_cmd.long"), helpWidth),
		Example: "  greenlens help measure\n  greenlens help attribution\n  greenlens help GL1002",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && help.IsCode(args[0]) {
				return a.explainCode(cmd, help.Code(strings.ToUpper(args[0])))
			}
			if len(args) == 1 && isTopic(strings.ToLower(args[0])) {
				return a.writeTopic(cmd, strings.ToLower(args[0]))
			}
			if len(args) == 0 {
				if err := cmd.Root().Help(); err != nil {
					return err
				}
				return a.writeTopics(cmd)
			}
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

// explainCode prints the documentation of an error code.
func (a *app) explainCode(cmd *cobra.Command, c help.Code) error {
	if !help.Known(c) {
		return help.New(help.GL0010, "code", string(c))
	}
	e := help.New(c)
	l := a.l
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n\n%s\n  %s\n\n%s\n  %s\n\n%s %d\n",
		c, e.Message(l), l.T("cli.help_code.cause"), wrap(e.Cause(l), helpWidth-2), l.T("cli.help_code.fix"), wrap(e.Fix(l), helpWidth-2),
		l.T("cli.help_code.exit"), e.ExitCode())
	return err
}
