// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/MarioUrenaGarcia/greenlens/internal/client"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/output"
	"github.com/MarioUrenaGarcia/greenlens/internal/paths"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
	"github.com/MarioUrenaGarcia/greenlens/internal/units"
)

// BackupReport is the document of greenlens backup and greenlens restore.
type BackupReport struct {
	SchemaVersion  int    `json:"schema_version"`
	File           string `json:"file"`
	Database       string `json:"database,omitempty"`
	AgentSocket    string `json:"agent_socket,omitempty"`
	Bytes          int64  `json:"bytes"`
	DatabaseSchema int    `json:"database_schema_version"`
	Kept           string `json:"previous_database,omitempty"`
}

func newBackupCmd(a *app) *cobra.Command {
	l := a.l
	var database string
	cmd := &cobra.Command{
		Use:   "backup <" + l.T("cli.backup.placeholder") + ">",
		Short: l.T("cli.backup.short"),
		Long:  wrap(l.T("cli.backup.long"), helpWidth),
		Example: "  greenlens backup greenlens-2026-10-20.db\n" +
			"  greenlens backup --database /var/lib/greenlens/greenlens.db /srv/backups/greenlens.db",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			file := args[0]
			if _, err := os.Lstat(file); err == nil {
				return help.New(help.GL4007, "path", file, "detail", l.T("cli.backup.exists"))
			}
			cfg, _, err := a.config()
			if err != nil {
				return err
			}
			if database == "" {
				if c, ok := a.runningAgent(ctx, cfg); ok {
					rep, err := backupFromAgent(ctx, c, file)
					if err != nil {
						return err
					}
					return a.writeBackup(cmd, rep)
				}
				if database, err = paths.Database(cfg, a.getenv); err != nil {
					return help.New(help.GL4002, "path", "agent.data_dir", "detail", err.Error()).Wrap(err)
				}
			}
			if _, err := os.Stat(database); err != nil {
				return help.New(help.GL4002, "path", database, "detail", err.Error()).Wrap(err)
			}
			info, err := store.BackupFile(ctx, database, file)
			if err != nil {
				return help.New(help.GL4007, "path", file, "detail", err.Error()).Wrap(err)
			}
			return a.writeBackup(cmd, BackupReport{SchemaVersion: 1, File: file, Database: database, Bytes: info.Bytes, DatabaseSchema: info.SchemaVersion})
		},
	}
	cmd.Flags().StringVar(&database, "database", "", l.T("cli.backup.flag.database"))
	return cmd
}

func fileExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// runningAgent returns the agent when one answers within two seconds.
func (a *app) runningAgent(ctx context.Context, cfg config.Config) (*client.Client, bool) {
	actx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	c, err := a.agent(actx, cfg)
	return c, err == nil
}

// backupFromAgent receives the copy the agent makes, which works for an
// agent running as another user, and checks it before giving it its name.
func backupFromAgent(ctx context.Context, c *client.Client, file string) (BackupReport, error) {
	tmp, err := store.TempFile(file)
	if err != nil {
		return BackupReport{}, help.New(help.GL4007, "path", file, "detail", err.Error()).Wrap(err)
	}
	fail := func(err error) (BackupReport, error) {
		store.RemoveDB(tmp)
		var he *help.Error
		if errors.As(err, &he) {
			return BackupReport{}, err
		}
		return BackupReport{}, help.New(help.GL4007, "path", file, "detail", err.Error()).Wrap(err)
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // the temporary file was just created next to the chosen file
	if err != nil {
		return fail(err)
	}
	_, err = c.Backup(ctx, f)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fail(err)
	}
	info, err := store.Validate(ctx, tmp)
	if err != nil {
		return fail(err)
	}
	if err := store.MoveNew(tmp, file); err != nil {
		return fail(err)
	}
	return BackupReport{SchemaVersion: 1, File: file, AgentSocket: c.Socket, Bytes: info.Bytes, DatabaseSchema: info.SchemaVersion}, nil
}

func (a *app) writeBackup(cmd *cobra.Command, rep BackupReport) error {
	l := a.l
	if a.out() != output.Table {
		g := output.Grid{Header: []string{"file", "database", "agent_socket", "bytes", "database_schema_version"},
			Rows: [][]string{{rep.File, rep.Database, rep.AgentSocket, fmt.Sprint(rep.Bytes), fmt.Sprint(rep.DatabaseSchema)}}}
		return output.Write(cmd.OutOrStdout(), a.out(), rep, g)
	}
	w := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(w, l.T("cli.backup.done", "file", rep.File, "size", units.Bytes(rep.Bytes, a.raw), "schema", rep.DatabaseSchema))
	from := l.T("cli.backup.from_database", "database", rep.Database)
	if rep.AgentSocket != "" {
		from = l.T("cli.backup.from_agent", "socket", rep.AgentSocket)
	}
	_, err := fmt.Fprintln(w, from)
	return err
}

func newRestoreCmd(a *app) *cobra.Command {
	l := a.l
	var database string
	var noKeep bool
	cmd := &cobra.Command{
		Use:   "restore <" + l.T("cli.backup.placeholder") + ">",
		Short: l.T("cli.restore.short"),
		Long:  wrap(l.T("cli.restore.long"), helpWidth),
		Example: "  greenlens restore greenlens-2026-10-20.db\n" +
			"  sudo -u greenlens greenlens restore --database /var/lib/greenlens/greenlens.db /srv/backups/greenlens.db",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			file := args[0]
			if _, err := store.Validate(ctx, file); err != nil {
				return backupError(file, err)
			}
			cfg, _, err := a.config()
			if err != nil {
				return err
			}
			if c, ok := a.runningAgent(ctx, cfg); ok {
				return help.New(help.GL4009, "socket", c.Socket)
			}
			if database == "" {
				if database, err = paths.Database(cfg, a.getenv); err != nil {
					return help.New(help.GL4002, "path", "agent.data_dir", "detail", err.Error()).Wrap(err)
				}
			}
			keep := ""
			if !noKeep && fileExists(database) {
				keep = database + ".before-restore-" + time.Now().Format("20060102-150405")
			}
			info, err := store.Restore(ctx, database, file, keep)
			if err != nil {
				if errors.Is(err, store.ErrInvalidBackup) {
					return backupError(file, err)
				}
				return help.New(help.GL4002, "path", database, "detail", err.Error()).Wrap(err)
			}
			return a.writeRestore(cmd, BackupReport{SchemaVersion: 1, File: file, Database: database, Bytes: info.Bytes, DatabaseSchema: info.SchemaVersion, Kept: keep})
		},
	}
	cmd.Flags().StringVar(&database, "database", "", l.T("cli.restore.flag.database"))
	cmd.Flags().BoolVar(&noKeep, "no-keep", false, l.T("cli.restore.flag.no_keep"))
	return cmd
}

// backupError explains why a file cannot be restored.
func backupError(file string, err error) error {
	var newer *store.NewerSchemaError
	if errors.As(err, &newer) {
		return help.New(help.GL4010, "path", file, "version", newer.Version, "supported", newer.Supported).Wrap(err)
	}
	return help.New(help.GL4008, "path", file, "detail", err.Error()).Wrap(err)
}

func (a *app) writeRestore(cmd *cobra.Command, rep BackupReport) error {
	l := a.l
	if a.out() != output.Table {
		g := output.Grid{Header: []string{"file", "database", "bytes", "database_schema_version", "previous_database"},
			Rows: [][]string{{rep.File, rep.Database, fmt.Sprint(rep.Bytes), fmt.Sprint(rep.DatabaseSchema), rep.Kept}}}
		return output.Write(cmd.OutOrStdout(), a.out(), rep, g)
	}
	w := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(w, l.T("cli.restore.done", "file", rep.File, "database", rep.Database, "size", units.Bytes(rep.Bytes, a.raw), "schema", rep.DatabaseSchema))
	if rep.Kept != "" {
		_, _ = fmt.Fprintln(w, wrap(l.T("cli.restore.kept", "path", rep.Kept), helpWidth))
	}
	_, err := fmt.Fprintln(w, l.T("cli.restore.start"))
	return err
}
