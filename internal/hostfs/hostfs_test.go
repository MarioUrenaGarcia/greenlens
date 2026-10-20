// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package hostfs

import (
	"slices"
	"testing"
	"testing/fstest"
)

func TestCPUList(t *testing.T) {
	tests := map[string][]int{
		"":            nil,
		"0":           {0},
		"0-3":         {0, 1, 2, 3},
		"0,24":        {0, 24},
		"0-1,8,10-11": {0, 1, 8, 10, 11},
	}
	for in, want := range tests {
		got, err := CPUList(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("CPUList(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"a", "3-1", "1-", "-1", "1,,2"} {
		if _, err := CPUList(bad); err == nil {
			t.Errorf("CPUList(%q) accepted", bad)
		}
	}
}

func FuzzCPUList(f *testing.F) {
	f.Add("0-3,8")
	f.Fuzz(func(t *testing.T, s string) {
		cpus, err := CPUList(s)
		if err == nil && len(cpus) > 0 && cpus[0] < 0 {
			t.Fatalf("negative cpu from %q", s)
		}
	})
}

func TestReaders(t *testing.T) {
	fsys := fstest.MapFS{
		"a/n":   {Data: []byte("42\n")},
		"a/neg": {Data: []byte("-7\n")},
		"a/s":   {Data: []byte("  text \n")},
		"a/bad": {Data: []byte("4x\n")},
		"d/x/f": {Data: []byte("1")},
		"d/y":   {Data: []byte("1")},
	}
	if v, err := Uint64(fsys, "a/n"); err != nil || v != 42 {
		t.Errorf("Uint64 = %d, %v", v, err)
	}
	if v, err := Int64(fsys, "a/neg"); err != nil || v != -7 {
		t.Errorf("Int64 = %d, %v", v, err)
	}
	if s, err := String(fsys, "a/s"); err != nil || s != "text" {
		t.Errorf("String = %q, %v", s, err)
	}
	if _, err := Uint64(fsys, "a/bad"); err == nil {
		t.Error("bad number accepted")
	}
	if !Exists(fsys, "a/n") || Exists(fsys, "a/none") {
		t.Error("Exists")
	}
	dirs, err := Dirs(fsys, "d")
	if err != nil || !slices.Equal(dirs, []string{"x"}) {
		t.Errorf("Dirs = %v, %v", dirs, err)
	}
	if dirs, err := Dirs(fsys, "missing"); err != nil || dirs != nil {
		t.Errorf("Dirs of a missing dir = %v, %v", dirs, err)
	}
}
