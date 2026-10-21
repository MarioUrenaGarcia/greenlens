// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"modernc.org/sqlite"
)

// ErrInvalidBackup is returned when a file is not a database of GreenLens
// or fails its integrity check.
var ErrInvalidBackup = errors.New("not a valid GreenLens database")

// NewerSchemaError is returned when a backup was made by a newer version,
// whose schema this one cannot read.
type NewerSchemaError struct {
	Version, Supported int
}

func (e *NewerSchemaError) Error() string {
	return fmt.Sprintf("schema version %d is newer than %d", e.Version, e.Supported)
}

// BackupInfo describes a validated backup.
type BackupInfo struct {
	SchemaVersion int   `json:"schema_version"`
	Bytes         int64 `json:"bytes"`
}

// tables are the tables every version of the schema has.
var tables = []string{"schema_migrations", "sources", "node_samples", "backends", "models", "usage_intervals", "benchmarks"}

// backuper is implemented by the connections of the SQLite driver.
type backuper interface {
	NewBackup(dst string) (*sqlite.Backup, error)
	NewRestore(src string) (*sqlite.Backup, error)
}

// LatestSchema returns the schema version this build writes.
func LatestSchema() (int, error) {
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return 0, err
	}
	latest := 0
	for _, e := range entries {
		num, _, _ := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(num)
		if err != nil {
			return 0, fmt.Errorf("migration %s: name must start with its number", e.Name())
		}
		latest = max(latest, v)
	}
	return latest, nil
}

// Backup writes a consistent copy of the database to dst, which must not
// exist. The copy is taken in one step of the SQLite backup API, so it
// reflects a single moment even while the agent keeps writing. It is made
// in a temporary file next to dst, checked, and only then given its name.
// The file is readable only by its owner.
func (s *Store) Backup(ctx context.Context, dst string) (BackupInfo, error) {
	return backupDB(ctx, s.db, dst)
}

// BackupFile copies the database at src to dst like Store.Backup, opening
// src read-only so that it is left as it is.
func BackupFile(ctx context.Context, src, dst string) (BackupInfo, error) {
	if _, err := os.Stat(src); err != nil {
		return BackupInfo{}, err
	}
	db, err := sql.Open("sqlite", fileURI(src, "mode=ro&_pragma=busy_timeout(5000)"))
	if err != nil {
		return BackupInfo{}, err
	}
	defer func() { _ = db.Close() }()
	return backupDB(ctx, db, dst)
}

func backupDB(ctx context.Context, db *sql.DB, dst string) (BackupInfo, error) {
	if _, err := os.Lstat(dst); err == nil {
		return BackupInfo{}, fmt.Errorf("%s: %w", dst, fs.ErrExist)
	}
	tmp, err := TempFile(dst)
	if err != nil {
		return BackupInfo{}, err
	}
	info, err := copyTo(ctx, db, tmp)
	if err == nil {
		err = MoveNew(tmp, dst)
	}
	if err != nil {
		RemoveDB(tmp)
		return BackupInfo{}, err
	}
	return info, nil
}

// TempFile reserves an empty file, readable only by its owner, in the
// directory of dst, where it can later be renamed to dst.
func TempFile(dst string) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*.partial")
	if err != nil {
		return "", err
	}
	return f.Name(), f.Close()
}

// MoveNew gives tmp the name dst, failing if dst exists. A hard link makes
// the check and the rename one step; file systems without hard links fall
// back to a check followed by a rename.
func MoveNew(tmp, dst string) error {
	err := os.Link(tmp, dst)
	if err == nil {
		return os.Remove(tmp)
	}
	if errors.Is(err, fs.ErrExist) {
		return err
	}
	if _, serr := os.Lstat(dst); serr == nil {
		return fmt.Errorf("%s: %w", dst, fs.ErrExist)
	}
	return os.Rename(tmp, dst)
}

// RemoveDB removes a database file with its journal files.
func RemoveDB(p string) {
	for _, sfx := range []string{"", "-journal", "-wal", "-shm"} {
		_ = os.Remove(p + sfx)
	}
}

func copyTo(ctx context.Context, db *sql.DB, dst string) (BackupInfo, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return BackupInfo{}, err
	}
	err = conn.Raw(func(dc any) error {
		b, ok := dc.(backuper)
		if !ok {
			return errors.New("the database driver cannot make backups")
		}
		bk, err := b.NewBackup(fileURI(dst, ""))
		if err != nil {
			return err
		}
		return step(bk)
	})
	if cerr := conn.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return BackupInfo{}, err
	}
	// The copy keeps the WAL flag of the live database, which would make
	// SQLite create -wal and -shm files next to it; a backup is a single
	// file.
	if err := setRollbackJournal(ctx, dst); err != nil {
		return BackupInfo{}, err
	}
	return Validate(ctx, dst)
}

// step copies every page in one call and releases the backup.
func step(bk *sqlite.Backup) error {
	_, err := bk.Step(-1)
	if ferr := bk.Finish(); err == nil {
		err = ferr
	}
	return err
}

func setRollbackJournal(ctx context.Context, p string) error {
	db, err := sql.Open("sqlite", fileURI(p, ""))
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `PRAGMA journal_mode=DELETE`)
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	return err
}

// Validate checks that p is a complete database of GreenLens that this
// version can read. It opens the file as immutable, so nothing is written
// next to it.
func Validate(ctx context.Context, p string) (BackupInfo, error) {
	st, err := os.Stat(p)
	if err != nil {
		return BackupInfo{}, err
	}
	if !st.Mode().IsRegular() {
		return BackupInfo{}, fmt.Errorf("%w: not a regular file", ErrInvalidBackup)
	}
	db, err := sql.Open("sqlite", fileURI(p, "immutable=1"))
	if err != nil {
		return BackupInfo{}, err
	}
	defer func() { _ = db.Close() }()
	var check string
	if err := db.QueryRowContext(ctx, `PRAGMA quick_check(1)`).Scan(&check); err != nil {
		return BackupInfo{}, fmt.Errorf("%w: %w", ErrInvalidBackup, err)
	}
	if check != "ok" {
		return BackupInfo{}, fmt.Errorf("%w: %s", ErrInvalidBackup, check)
	}
	for _, t := range tables {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = ?`, t).Scan(&n); err != nil {
			return BackupInfo{}, fmt.Errorf("%w: %w", ErrInvalidBackup, err)
		}
		if n == 0 {
			return BackupInfo{}, fmt.Errorf("%w: table %s is missing", ErrInvalidBackup, t)
		}
	}
	var v sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&v); err != nil {
		return BackupInfo{}, fmt.Errorf("%w: %w", ErrInvalidBackup, err)
	}
	if !v.Valid {
		return BackupInfo{}, fmt.Errorf("%w: no schema version", ErrInvalidBackup)
	}
	latest, err := LatestSchema()
	if err != nil {
		return BackupInfo{}, err
	}
	if int(v.Int64) > latest {
		return BackupInfo{}, &NewerSchemaError{Version: int(v.Int64), Supported: latest}
	}
	return BackupInfo{SchemaVersion: int(v.Int64), Bytes: st.Size()}, nil
}

// Restore replaces the database at dbPath with the backup at src, after
// validating it. When a database already exists, a copy of it is first
// written to keep, unless keep is empty. The pages are written through
// SQLite, so a write-ahead log left by the previous database cannot be
// replayed over the restored one. Migrations newer than the backup are
// applied afterwards.
func Restore(ctx context.Context, dbPath, src, keep string) (BackupInfo, error) {
	info, err := Validate(ctx, src)
	if err != nil {
		return BackupInfo{}, err
	}
	absDB, err1 := filepath.Abs(dbPath)
	absSrc, err2 := filepath.Abs(src)
	if err1 == nil && err2 == nil && absDB == absSrc {
		return BackupInfo{}, errors.New("the backup is the database itself")
	}
	_, statErr := os.Stat(dbPath)
	existed := statErr == nil
	s, err := Open(ctx, dbPath)
	if err != nil {
		return BackupInfo{}, err
	}
	defer func() { _ = s.Close() }()
	if existed && keep != "" {
		if _, err := s.Backup(ctx, keep); err != nil {
			return BackupInfo{}, fmt.Errorf("keeping the current database: %w", err)
		}
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return BackupInfo{}, err
	}
	err = conn.Raw(func(dc any) error {
		b, ok := dc.(backuper)
		if !ok {
			return errors.New("the database driver cannot restore backups")
		}
		bk, err := b.NewRestore(fileURI(src, "immutable=1"))
		if err != nil {
			return err
		}
		return step(bk)
	})
	if cerr := conn.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return BackupInfo{}, err
	}
	// The restored pages carry the journal mode of the backup; the agent
	// expects WAL, which the next Open sets again.
	if _, err := s.db.ExecContext(ctx, `PRAGMA journal_mode=WAL`); err != nil {
		return BackupInfo{}, err
	}
	if err := s.migrate(ctx); err != nil {
		return BackupInfo{}, err
	}
	return info, nil
}

// fileURI builds a SQLite URI for a path. The characters that end the path
// or start an escape in a URI are percent-encoded.
func fileURI(p, query string) string {
	r := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23")
	u := "file:" + r.Replace(p)
	if query != "" {
		u += "?" + query
	}
	return u
}
