// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

//go:build linux

package sandbox

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const childEnv = "GREENLENS_SANDBOX_TEST_CHILD"

// The restriction is irreversible and applies to the whole process, so the
// checks run in a child copy of the test binary.
func TestRestrict(t *testing.T) {
	ro, rw, outside := t.TempDir(), t.TempDir(), t.TempDir()
	for _, p := range []string{filepath.Join(ro, "f"), filepath.Join(outside, "f")} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRestrictChild$", "-test.v") //nolint:gosec // re-runs this test binary
	cmd.Env = append(os.Environ(), childEnv+"=1", "RO="+ro, "RW="+rw, "OUTSIDE="+outside)
	out, err := cmd.CombinedOutput()
	if strings.Contains(string(out), "landlock unavailable") {
		t.Skip("the kernel does not provide Landlock")
	}
	if err != nil {
		t.Fatalf("child failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "--- PASS: TestRestrictChild") {
		t.Fatalf("child did not run the checks:\n%s", out)
	}
}

func TestRestrictChild(t *testing.T) {
	if os.Getenv(childEnv) != "1" {
		t.Skip("runs only as the child of TestRestrict")
	}
	ro, rw, outside := os.Getenv("RO"), os.Getenv("RW"), os.Getenv("OUTSIDE")
	st, err := Restrict(Rules{ReadOnly: []string{ro}, ReadWrite: []string{rw}})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Enforced {
		t.Log("landlock unavailable")
		return
	}
	if _, err := os.ReadFile(filepath.Join(ro, "f")); err != nil {
		t.Errorf("read inside a read-only tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ro, "g"), []byte("x"), 0o600); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("write inside a read-only tree: %v, want permission denied", err)
	}
	if err := os.WriteFile(filepath.Join(rw, "g"), []byte("x"), 0o600); err != nil {
		t.Errorf("write inside a read-write tree: %v", err)
	}
	if _, err := os.ReadFile(filepath.Join(outside, "f")); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("read outside the rules: %v, want permission denied", err)
	}
	if _, err := os.ReadFile("/etc/hostname"); !errors.Is(err, fs.ErrPermission) && !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("read /etc/hostname: %v, want permission denied", err)
	}
}
