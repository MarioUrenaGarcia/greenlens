// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

//go:build linux

package sandbox

import (
	"errors"
	"io/fs"
	"net"
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

// resolveUnixABI is the first Landlock ABI that controls connecting to
// pathname Unix sockets.
const resolveUnixABI = 9

func TestSockets(t *testing.T) {
	// Socket paths are limited to about 100 bytes.
	dir, err := os.MkdirTemp("", "gls")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	for _, name := range []string{"allowed", "other"} {
		c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: filepath.Join(dir, name), Net: "unixgram"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSocketsChild$", "-test.v") //nolint:gosec // re-runs this test binary
	cmd.Env = append(os.Environ(), childEnv+"=1", "SOCKDIR="+dir)
	out, err := cmd.CombinedOutput()
	if strings.Contains(string(out), "landlock unavailable") {
		t.Skip("the kernel does not provide Landlock")
	}
	if err != nil || !strings.Contains(string(out), "--- PASS: TestSocketsChild") {
		t.Fatalf("child failed: %v\n%s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "landlock abi") {
			t.Log(strings.TrimSpace(line))
		}
	}
}

func TestSocketsChild(t *testing.T) {
	if os.Getenv(childEnv) != "1" {
		t.Skip("runs only as the child of TestSockets")
	}
	dir := os.Getenv("SOCKDIR")
	st, err := Restrict(Rules{Sockets: []string{filepath.Join(dir, "allowed"), filepath.Join(dir, "missing")}})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Enforced {
		t.Log("landlock unavailable")
		return
	}
	t.Logf("landlock abi %d", st.ABI)
	send := func(name string) error {
		var d net.Dialer
		c, err := d.DialContext(t.Context(), "unixgram", filepath.Join(dir, name)) //nolint:gosec // the sockets this test created
		if err != nil {
			return err
		}
		defer func() { _ = c.Close() }()
		_, err = c.Write([]byte("x"))
		return err
	}
	if err := send("allowed"); err != nil {
		t.Errorf("send to a listed socket: %v", err)
	}
	if err := send("other"); st.ABI >= resolveUnixABI && !errors.Is(err, fs.ErrPermission) {
		t.Errorf("send to another socket with ABI %d: %v, want permission denied", st.ABI, err)
	}
}
