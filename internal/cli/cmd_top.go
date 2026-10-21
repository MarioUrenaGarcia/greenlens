// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"encoding/csv"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/procfs"
	"github.com/MarioUrenaGarcia/greenlens/internal/tui"
)

func newTopCmd(a *app) *cobra.Command {
	l := a.l
	var refresh time.Duration
	var ascii bool
	cmd := &cobra.Command{
		Use:     "top",
		Short:   l.T("cli.top.short"),
		Long:    wrap(l.T("cli.top.long"), helpWidth),
		Example: "  greenlens top\n  greenlens top --refresh 5s --ascii",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if refresh < 250*time.Millisecond || refresh > time.Minute {
				return help.New(help.GL0005, "flag", "--refresh", "value", refresh, "command", cmd.CommandPath())
			}
			if !a.terminal() {
				return help.New(help.GL0016)
			}
			cfg, _, err := a.config()
			if err != nil {
				return err
			}
			c, err := a.agent(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			m := tui.New(tui.Options{
				Source: c, L: l, Refresh: refresh,
				Color: a.color(), Unicode: !ascii && utf8Locale(a.getenv), Raw: a.raw,
				Export: exportCSV, Self: a.selfUsage,
			})
			_, err = tea.NewProgram(m, tea.WithContext(cmd.Context()), tea.WithOutput(a.stdout)).Run()
			return err
		},
	}
	cmd.Flags().DurationVar(&refresh, "refresh", time.Second, l.T("cli.top.flag.refresh"))
	cmd.Flags().BoolVar(&ascii, "ascii", false, l.T("cli.top.flag.ascii"))
	return cmd
}

// terminal reports whether the standard output is a terminal.
func (a *app) terminal() bool {
	f, ok := a.stdout.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// utf8Locale reports whether the locale can show Unicode, from the
// variables in the order the C library reads them.
func utf8Locale(getenv func(string) string) bool {
	for _, v := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if s := getenv(v); s != "" {
			s = strings.ToLower(s)
			return strings.Contains(s, "utf-8") || strings.Contains(s, "utf8")
		}
	}
	return false
}

// exportCSV writes a view to a new file in the current directory.
func exportCSV(name string, rows [][]string) (string, error) {
	path := fmt.Sprintf("greenlens-%s-%s.csv", name, time.Now().Format("20060102-150405"))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // a new file in the current directory, named by the program
	if err != nil {
		return "", err
	}
	w := csv.NewWriter(f)
	if err := w.WriteAll(rows); err != nil {
		_ = f.Close()
		return "", err
	}
	return path, f.Close()
}

// selfUsage reports the CPU time and memory of this process.
func (a *app) selfUsage() (uint64, uint64, bool) {
	p, err := procfs.Process(a.selfFS(), os.Getpid())
	if err != nil {
		return 0, 0, false
	}
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return p.CPU(), 0, true
	}
	var size, resident uint64
	if _, err := fmt.Sscan(string(b), &size, &resident); err != nil {
		return p.CPU(), 0, true
	}
	return p.CPU(), resident * uint64(os.Getpagesize()), true //nolint:gosec // the page size is positive
}
