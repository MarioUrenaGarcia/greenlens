// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package store

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

func audits(t *testing.T, p string) int {
	t.Helper()
	db, err := sql.Open("sqlite", fileURI(p, "mode=ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM audit_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBackupIsOneConsistentFile(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for range 3 {
		if err := s.Audit(ctx, "uid:1000", "calibrate", "test"); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().Add(-time.Minute)
	if err := s.EnsureChannels(ctx, []source.Channel{pkg, dram}, at); err != nil {
		t.Fatal(err)
	}
	insert(t, s, at, 10, 2)
	dst := filepath.Join(t.TempDir(), "copy?#%.db")
	info, err := s.Backup(ctx, dst)
	if err != nil {
		t.Fatal(err)
	}
	latest, _ := LatestSchema()
	if info.SchemaVersion != latest || info.Bytes == 0 {
		t.Fatalf("info %+v, latest %d", info, latest)
	}
	st, err := os.Stat(dst)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", st, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(dst))
	if len(entries) != 1 {
		t.Fatalf("files next to the backup: %v", entries)
	}
	if n := audits(t, dst); n != 3 {
		t.Fatalf("audit rows %d", n)
	}
	if _, err := s.Backup(ctx, dst); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second backup over the first: %v", err)
	}
	// The database left by the agent is copied without opening it for writing.
	again := filepath.Join(t.TempDir(), "again.db")
	if _, err := BackupFile(ctx, s.Path(), again); err != nil {
		t.Fatal(err)
	}
	if n := audits(t, again); n != 3 {
		t.Fatalf("audit rows of the file copy %d", n)
	}
}

func TestValidateRejectsOtherFiles(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	text := filepath.Join(dir, "notes.db")
	if err := os.WriteFile(text, []byte("not a database, just some text that is long enough to have a header"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other.db")
	db, err := sql.Open("sqlite", other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE t (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	for _, p := range []string{text, other, dir} {
		if _, err := Validate(ctx, p); !errors.Is(err, ErrInvalidBackup) {
			t.Errorf("%s: %v", filepath.Base(p), err)
		}
	}
	if _, err := Validate(ctx, filepath.Join(dir, "missing.db")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}

	s := open(t)
	newer := filepath.Join(dir, "newer.db")
	if _, err := s.Backup(ctx, newer); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", newer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (99, 0)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	var ne *NewerSchemaError
	if _, err := Validate(ctx, newer); !errors.As(err, &ne) || ne.Version != 99 {
		t.Fatalf("newer schema: %v", err)
	}
}

func TestRestoreReplacesAndKeeps(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := open(t)
	if err := s.Audit(ctx, "uid:1000", "calibrate", "in the backup"); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "backup.db")
	if _, err := s.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}

	// The current database has more rows and a write-ahead log that must
	// not be replayed over the restored pages.
	target := filepath.Join(dir, "data", "greenlens.db")
	cur, err := Open(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if err := cur.Audit(ctx, "uid:1000", "benchmark", "current"); err != nil {
			t.Fatal(err)
		}
	}
	wal, err := os.ReadFile(target + "-wal")
	if err != nil || len(wal) == 0 {
		t.Fatalf("no write-ahead log: %v", err)
	}
	_ = cur.Close()
	// An agent that stopped without closing the database leaves its log.
	if err := os.WriteFile(target+"-wal", wal, 0o600); err != nil {
		t.Fatal(err)
	}

	keep := target + ".before-restore"
	if _, err := Restore(ctx, target, backup, keep); err != nil {
		t.Fatal(err)
	}
	if n := audits(t, target); n != 1 {
		t.Fatalf("restored audit rows %d", n)
	}
	if n := audits(t, keep); n != 5 {
		t.Fatalf("kept audit rows %d", n)
	}
	r, err := Open(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := r.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal mode %q %v", mode, err)
	}
	_ = r.Close()

	// Into a data directory that does not exist yet, nothing is kept.
	fresh := filepath.Join(dir, "fresh", "greenlens.db")
	if _, err := Restore(ctx, fresh, backup, ""); err != nil {
		t.Fatal(err)
	}
	if n := audits(t, fresh); n != 1 {
		t.Fatalf("fresh audit rows %d", n)
	}
	if _, err := Restore(ctx, backup, backup, ""); err == nil {
		t.Fatal("restored a backup over itself")
	}
	if _, err := Restore(ctx, target, filepath.Join(dir, "missing.db"), ""); err == nil {
		t.Fatal("restored a missing file")
	}
}
