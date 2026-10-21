// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

// offlineConfig points at a data directory and a socket nobody listens on,
// so no agent of the machine running the tests is found.
func offlineConfig(t *testing.T) (cfgPath, db string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath = filepath.Join(dir, "greenlens.yaml")
	text := "agent:\n  data_dir: " + filepath.Join(dir, "data") + "\n  socket: " + filepath.Join(dir, "none.sock") + "\n"
	if err := os.WriteFile(cfgPath, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath, filepath.Join(dir, "data", "greenlens.db")
}

func TestBackupAndRestoreWithoutAgent(t *testing.T) {
	ctx := context.Background()
	cfgPath, db := offlineConfig(t)
	dir := filepath.Dir(cfgPath)
	file := filepath.Join(dir, "backup.db")

	if code, _, errOut := runWith(t, hooks{}, nil, "backup", "--config", cfgPath, file); code != 1 || !strings.Contains(errOut, "GL4002") {
		t.Fatalf("backup without a database: %d %s", code, errOut)
	}
	s, err := store.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Audit(ctx, "uid:1000", "calibrate", "test"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	code, out, errOut := runWith(t, hooks{}, nil, "backup", "--config", cfgPath, file)
	if code != 0 || !strings.Contains(out, "Backup written to "+file) || !strings.Contains(out, "Copied from "+db) {
		t.Fatalf("backup: %d %s %s", code, out, errOut)
	}
	if code, _, errOut := runWith(t, hooks{}, map[string]string{"GREENLENS_LANG": "es"}, "backup", "--config", cfgPath, file); code != 1 ||
		!strings.Contains(errOut, "GL4007") || !strings.Contains(errOut, "el archivo ya existe") {
		t.Fatalf("backup over a file: %d %s", code, errOut)
	}

	code, out, errOut = runWith(t, hooks{}, nil, "restore", "--config", cfgPath, file, "-o", "json")
	var rep BackupReport
	if code != 0 || json.Unmarshal([]byte(out), &rep) != nil || rep.Database != db || rep.Kept == "" {
		t.Fatalf("restore: %d %s %s", code, out, errOut)
	}
	if _, err := os.Stat(rep.Kept); err != nil {
		t.Fatalf("previous database not kept: %v", err)
	}
	code, out, _ = runWith(t, hooks{}, map[string]string{"GREENLENS_LANG": "es"}, "restore", "--config", cfgPath, "--no-keep", file)
	if code != 0 || !strings.Contains(out, "Se restauró") || strings.Contains(out, "quedó en") {
		t.Fatalf("restore without keeping: %d %s", code, out)
	}

	junk := filepath.Join(dir, "junk.db")
	if err := os.WriteFile(junk, []byte(strings.Repeat("x", 4096)), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runWith(t, hooks{}, nil, "restore", "--config", cfgPath, junk); code != 1 || !strings.Contains(errOut, "GL4008") {
		t.Fatalf("restore of a text file: %d %s", code, errOut)
	}
	newer := filepath.Join(dir, "newer.db")
	if code, _, errOut := runWith(t, hooks{}, nil, "backup", "--config", cfgPath, newer); code != 0 {
		t.Fatalf("backup: %d %s", code, errOut)
	}
	sdb, err := sql.Open("sqlite", newer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sdb.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (99, 0)`); err != nil {
		t.Fatal(err)
	}
	_ = sdb.Close()
	if code, _, errOut := runWith(t, hooks{}, nil, "restore", "--config", cfgPath, newer); code != 1 || !strings.Contains(errOut, "GL4010") {
		t.Fatalf("restore of a newer schema: %d %s", code, errOut)
	}
	if code, _, errOut := runWith(t, hooks{}, nil, "restore", "--config", cfgPath); code != 2 || !strings.Contains(errOut, "GL0015") {
		t.Fatalf("restore without a file: %d %s", code, errOut)
	}
}
