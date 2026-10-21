// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package manpage writes the manual pages of greenlens in roff: one page in
// section 1 for the program and one for every command, and greenlens.yaml
// in section 5 for the configuration file. Pages are written in the
// language of the localizer they are given.
package manpage

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/i18n"
)

// Page is one manual page.
type Page struct {
	// Name is the file name, such as greenlens-status.1.
	Name    string
	Content string
}

// Header describes every page.
type Header struct {
	// Date is the date of the release, as YYYY-MM-DD.
	Date    string
	Version string
}

// escape makes text safe in roff: backslashes, lines that would start with
// a control character, and characters outside ASCII, written as \[uXXXX] so
// the pages read the same with every man implementation.
func escape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\e`)
	var b strings.Builder
	for _, r := range s {
		if r < 0x80 {
			b.WriteRune(r)
			continue
		}
		fmt.Fprintf(&b, `\[u%04X]`, r)
	}
	s = b.String()
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, ".") || strings.HasPrefix(l, "'") {
			lines[i] = `\&` + l
		}
	}
	return strings.Join(lines, "\n")
}

// dashes writes the hyphens of option names as roff minus signs, so they
// can be searched and copied.
func dashes(s string) string { return strings.ReplaceAll(escape(s), "-", `\-`) }

func pageName(cmd *cobra.Command) string {
	return strings.ReplaceAll(cmd.CommandPath(), " ", "-")
}

func th(name string, section int, h Header, l *i18n.Localizer) string {
	return fmt.Sprintf(".TH %s %d \"%s\" \"%s\" \"%s\"\n", strings.ToUpper(name), section, escape(h.Date), escape("greenlens "+h.Version), escape(l.T("man.manual")))
}

// Commands returns the page of root and of every command below it that is
// shown in the help.
func Commands(root *cobra.Command, l *i18n.Localizer, h Header) []Page {
	var out []Page
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		out = append(out, Page{Name: pageName(c) + ".1", Content: command(c, l, h)})
		for _, sub := range c.Commands() {
			if sub.IsAvailableCommand() || sub.Name() == "help" {
				walk(sub)
			}
		}
	}
	walk(root)
	return out
}

func flags(fs *pflag.FlagSet) string {
	var b strings.Builder
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		b.WriteString(".TP\n")
		name := `\fB\-\-` + dashes(f.Name) + `\fR`
		if f.Shorthand != "" {
			name = `\fB\-` + dashes(f.Shorthand) + `\fR, ` + name
		}
		// UnquoteUsage takes a `word` of the description as the name of the
		// value, as the help of the command does, and drops the quotes.
		varname, usage := pflag.UnquoteUsage(f)
		if varname != "" {
			name += ` \fI` + escape(varname) + `\fR`
		}
		b.WriteString(name + "\n")
		b.WriteString(escape(usage))
		if d := f.DefValue; d != "" && d != "false" && d != "[]" && d != "0s" && d != "0" {
			b.WriteString(" (" + escape(d) + ")")
		}
		b.WriteString("\n")
	})
	return b.String()
}

func command(c *cobra.Command, l *i18n.Localizer, h Header) string {
	var b strings.Builder
	name := pageName(c)
	b.WriteString(th(name, 1, h, l))
	b.WriteString(".SH " + escape(l.T("man.name")) + "\n")
	b.WriteString(dashes(name) + ` \- ` + escape(c.Short) + "\n")
	b.WriteString(".SH " + escape(l.T("man.synopsis")) + "\n")
	use := strings.SplitN(c.Use, " ", 2)
	b.WriteString(`.B ` + dashes(c.CommandPath()) + "\n")
	if len(use) == 2 {
		b.WriteString(escape(use[1]) + "\n")
	}
	if c.HasAvailableFlags() {
		b.WriteString(`[\fI` + escape(l.T("cli.usage.flags_placeholder")) + `\fR]` + "\n")
	}
	if c.HasAvailableSubCommands() {
		b.WriteString(`\fI` + escape(l.T("cli.usage.command_placeholder")) + `\fR` + "\n")
	}
	b.WriteString(".SH " + escape(l.T("man.description")) + "\n")
	desc := c.Long
	if desc == "" {
		desc = c.Short
	}
	for _, p := range strings.Split(strings.ReplaceAll(desc, "\n", " "), "  ") {
		b.WriteString(escape(strings.TrimSpace(p)) + "\n")
	}
	if c.HasAvailableSubCommands() {
		b.WriteString(".SH " + escape(l.T("man.commands")) + "\n")
		for _, sub := range c.Commands() {
			if sub.IsAvailableCommand() || sub.Name() == "help" {
				b.WriteString(".TP\n" + `\fB` + dashes(sub.Name()) + `\fR` + "\n" + escape(sub.Short) + "\n")
			}
		}
	}
	if c.HasAvailableLocalFlags() {
		b.WriteString(".SH " + escape(l.T("man.options")) + "\n" + flags(c.LocalFlags()))
	}
	if c.HasAvailableInheritedFlags() {
		b.WriteString(".SH " + escape(l.T("man.global_options")) + "\n" + flags(c.InheritedFlags()))
	}
	if c.Example != "" {
		b.WriteString(".SH " + escape(l.T("man.examples")) + "\n.nf\n")
		for _, line := range strings.Split(c.Example, "\n") {
			b.WriteString(escape(strings.TrimPrefix(line, "  ")) + "\n")
		}
		b.WriteString(".fi\n")
	}
	b.WriteString(".SH " + escape(l.T("man.see_also")) + "\n")
	var see []string
	if p := c.Parent(); p != nil {
		see = append(see, `\fB`+dashes(pageName(p))+`\fR(1)`)
	}
	for _, sub := range c.Commands() {
		if sub.IsAvailableCommand() {
			see = append(see, `\fB`+dashes(pageName(sub))+`\fR(1)`)
		}
	}
	if c.Parent() == nil {
		see = append(see, `\fBgreenlens.yaml\fR(5)`)
	}
	b.WriteString(strings.Join(see, ",\n") + "\n")
	return b.String()
}

// Config returns greenlens.yaml(5): every key of the configuration file
// with its type, default, variable and description.
func Config(l *i18n.Localizer, h Header) Page {
	var b strings.Builder
	b.WriteString(th("greenlens.yaml", 5, h, l))
	b.WriteString(".SH " + escape(l.T("man.name")) + "\n")
	b.WriteString(`greenlens.yaml \- ` + escape(l.T("man.config.short")) + "\n")
	b.WriteString(".SH " + escape(l.T("man.synopsis")) + "\n")
	b.WriteString("/etc/greenlens/greenlens.yaml\n.br\n$XDG_CONFIG_HOME/greenlens/greenlens.yaml\n")
	b.WriteString(".SH " + escape(l.T("man.description")) + "\n" + escape(l.T("man.config.description")) + "\n")
	fields := config.Fields()
	sections := map[string][]config.Field{}
	var order []string
	for _, f := range fields {
		top := strings.TrimSuffix(strings.SplitN(f.Key, ".", 2)[0], "[]")
		if _, ok := sections[top]; !ok {
			order = append(order, top)
		}
		sections[top] = append(sections[top], f)
	}
	sort.Strings(order)
	for _, s := range order {
		b.WriteString(".SH " + strings.ToUpper(s) + "\n")
		for _, f := range sections[s] {
			b.WriteString(".TP\n" + `\fB` + dashes(f.Key) + `\fR (` + escape(f.Type) + ")\n")
			b.WriteString(escape(l.T(f.DocKey())) + "\n")
			var meta []string
			if f.Default != "" && f.Default != "empty" {
				meta = append(meta, l.T("man.config.default", "value", f.Default))
			}
			if f.Env != "" {
				meta = append(meta, l.T("man.config.env", "name", f.Env))
			}
			if len(meta) > 0 {
				b.WriteString(".br\n" + escape(strings.Join(meta, "; ")) + "\n")
			}
		}
	}
	b.WriteString(".SH " + escape(l.T("man.see_also")) + "\n" + `\fBgreenlens\fR(1), \fBgreenlens\-config\fR(1)` + "\n")
	return Page{Name: "greenlens.yaml.5", Content: b.String()}
}
