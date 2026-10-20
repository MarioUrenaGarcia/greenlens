// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"context"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/client"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/hostfs"
	"github.com/MarioUrenaGarcia/greenlens/internal/i18n"
	"github.com/MarioUrenaGarcia/greenlens/internal/output"
	"github.com/MarioUrenaGarcia/greenlens/internal/paths"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/estimate"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/registry"
)

// hooks replace the machine in tests.
type hooks struct {
	hostFS  fs.FS
	selfFS  fs.FS
	sources func(config.Config) []source.Source
	system  system
}

// app holds what every command shares: the localizer, the streams and the
// global flags.
type app struct {
	l      *i18n.Localizer
	stdout io.Writer
	stderr io.Writer
	getenv func(string) string
	hooks  hooks

	configPath string
	format     string
	quiet      bool
	verbose    bool
	noColor    bool
	raw        bool
}

func (a *app) addGlobalFlags(root *cobra.Command) {
	f := root.PersistentFlags()
	f.StringVarP(&a.format, "output", "o", string(output.Table), a.l.T("cli.flag.output"))
	f.BoolVarP(&a.quiet, "quiet", "q", false, a.l.T("cli.flag.quiet"))
	f.BoolVarP(&a.verbose, "verbose", "v", false, a.l.T("cli.flag.verbose"))
	f.StringVar(&a.configPath, "config", "", a.l.T("cli.flag.config"))
	f.BoolVar(&a.noColor, "no-color", false, a.l.T("cli.flag.no_color"))
	f.BoolVar(&a.raw, "raw", false, a.l.T("cli.flag.raw"))
}

// checkGlobalFlags runs before every command.
func (a *app) checkGlobalFlags() error {
	if _, err := output.Parse(a.format); err != nil {
		var names []string
		for _, f := range output.Formats {
			names = append(names, string(f))
		}
		return help.New(help.GL0009, "value", a.format, "formats", strings.Join(names, ", "))
	}
	return nil
}

func (a *app) out() output.Format {
	f, _ := output.Parse(a.format)
	return f
}

// config loads the configuration; every problem is returned.
func (a *app) config() (config.Config, string, error) {
	return config.Load(a.configPath, a.getenv)
}

func (a *app) hostFS(cfg config.Config) fs.FS {
	if a.hooks.hostFS != nil {
		return a.hooks.hostFS
	}
	return hostfs.Root(cfg.Agent.HostRoot)
}

func (a *app) selfFS() fs.FS {
	if a.hooks.selfFS != nil {
		return a.hooks.selfFS
	}
	return hostfs.Root("/")
}

func (a *app) sources(cfg config.Config) []source.Source {
	if a.hooks.sources != nil {
		return a.hooks.sources(cfg)
	}
	e := cfg.Sources.Estimate
	return registry.Sources(registry.Options{
		FS:       a.hostFS(cfg),
		HostRoot: cfg.Agent.HostRoot,
		Estimate: estimate.Override{IdleW: e.IdleWatts, FullW: e.FullWatts, Exponent: e.Exponent},
	})
}

// agent connects to the running agent.
func (a *app) agent(ctx context.Context, cfg config.Config) (*client.Client, error) {
	return client.Connect(ctx, paths.ClientSockets(cfg, a.getenv))
}

// say writes a line for people unless --quiet is set.
func (a *app) say(format string) {
	if !a.quiet {
		_, _ = io.WriteString(a.stderr, format+"\n")
	}
}

// color reports whether the output stream is a terminal that wants color.
func (a *app) color() bool {
	if a.noColor || a.getenv("NO_COLOR") != "" || a.getenv("TERM") == "dumb" {
		return false
	}
	f, ok := a.stdout.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// paint wraps s in an ANSI color when color is enabled. Amber marks
// warnings and green marks what is in order, as in the rest of GreenLens.
func (a *app) paint(s string, warn bool) string {
	if !a.color() {
		return s
	}
	code := "32"
	if warn {
		code = "33"
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func since(t time.Time) time.Duration { return time.Since(t).Round(time.Second) }

// hookSources returns the test sources, or nil so the agent builds the
// standard ones.
func (a *app) hookSources(cfg config.Config) []source.Source {
	if a.hooks.sources == nil {
		return nil
	}
	return a.hooks.sources(cfg)
}
