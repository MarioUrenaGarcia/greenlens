// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package procfs

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"testing/fstest"
)

func TestParseStat(t *testing.T) {
	line := "1234 (my (odd) cmd) S 1 1234 1234 0 -1 4194304 100 0 0 0 250 75 30 20 20 0 4 0 98765 1000 200 18446744073709551615\n"
	p, err := ParseStat([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	if p.PID != 1234 || p.Comm != "my (odd) cmd" || p.State != 'S' || p.PPID != 1 ||
		p.UTime != 250 || p.STime != 75 || p.CUTime != 30 || p.CSTime != 20 || p.StartTime != 98765 {
		t.Fatalf("parsed %+v", p)
	}
	if p.CPU() != 375 {
		t.Errorf("CPU() = %d", p.CPU())
	}
	for _, bad := range []string{"", "12 cmd S", "x (c) S 1", "12 (c) S 1 2 3"} {
		if _, err := ParseStat([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func FuzzParseStat(f *testing.F) {
	f.Add([]byte("1 (init) S 0 1 1 0 -1 4194560 1 2 3 4 5 6 7 8 20 0 1 0 9 10 11\n"))
	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = ParseStat(data)
	})
}

func TestSystemCPU(t *testing.T) {
	fsys := fstest.MapFS{"proc/stat": {Data: []byte("cpu  10 1 5 100 2 3 4 7 6 0\ncpu0 1 1 1 1 1 1 1 1 1 1\n")}}
	c, err := SystemCPU(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if c.Total() != 132 || c.Busy() != 23 {
		t.Errorf("total %d busy %d", c.Total(), c.Busy())
	}
}

func stat(pid, ppid int, utime, cutime uint64) []byte {
	return []byte(strconv.Itoa(pid) + " (p) S " + strconv.Itoa(ppid) + " 0 0 0 -1 0 0 0 0 0 " +
		strconv.FormatUint(utime, 10) + " 0 " + strconv.FormatUint(cutime, 10) + " 0 20 0 1 0 100 0 0\n")
}

func TestTreeCPUAcrossReaping(t *testing.T) {
	// Root 10 with child 11, which has child 12.
	before := fstest.MapFS{
		"proc/10/stat": {Data: stat(10, 1, 100, 0)},
		"proc/11/stat": {Data: stat(11, 10, 50, 0)},
		"proc/12/stat": {Data: stat(12, 11, 25, 0)},
		"proc/99/stat": {Data: stat(99, 1, 1000, 0)},
	}
	s, err := TakeSnapshot(before)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := s.TreeCPU(10); !ok || got != 175 {
		t.Fatalf("tree cpu %d, want 175", got)
	}
	// 12 exits and is reaped by 11: its time moves to the cutime of 11.
	after := fstest.MapFS{
		"proc/10/stat": {Data: stat(10, 1, 100, 0)},
		"proc/11/stat": {Data: stat(11, 10, 50, 25)},
	}
	s, err = TakeSnapshot(after)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.TreeCPU(10); got != 175 {
		t.Fatalf("tree cpu after reaping %d, want 175", got)
	}
	if _, ok := s.TreeCPU(42); ok {
		t.Error("missing root reported as found")
	}
}

func TestRealSelf(t *testing.T) {
	p, err := Process(os.DirFS("/"), os.Getpid())
	if err != nil {
		t.Skip("no /proc:", err)
	}
	if p.PID != os.Getpid() || filepath.Base(os.Args[0]) == "" {
		t.Fatalf("self %+v", p)
	}
}
