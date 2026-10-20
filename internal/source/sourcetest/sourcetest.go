// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package sourcetest gives tests access to the recorded host trees in
// testdata/hosts.
package sourcetest

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Host returns the recorded file system of a host fixture.
func Host(t testing.TB, name string) fs.FS {
	t.Helper()
	return os.DirFS(HostDir(t, name))
}

// HostDir returns the directory of a host fixture.
func HostDir(t testing.TB, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate testdata")
	}
	dir := filepath.Join(filepath.Dir(file), "..", "..", "..", "testdata", "hosts", name)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return dir
}

// Hosts lists every host fixture.
var Hosts = []string{
	"amd-zen4-desktop",
	"intel-client-1s",
	"intel-server-2s",
	"laptop-battery",
	"vm-no-sensors",
	"arm-ampere-altra",
	"server-acpi",
	"workstation-gpus-new",
	"workstation-gpus-old",
}
