// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package proctrack

import (
	"fmt"
	"testing"
	"testing/fstest"

	"github.com/MarioUrenaGarcia/greenlens/internal/procfs"
)

// host is a fake /proc. Each process has its own and reaped-child times.
type host struct {
	fs fstest.MapFS
}

type pstat struct {
	ppid          int
	start         uint64
	utime, cutime uint64
}

func (h host) set(pid int, p pstat) {
	line := fmt.Sprintf("%d (p%d) S %d 0 0 0 -1 0 0 0 0 0 %d 0 %d 0 20 0 1 0 %d 1000 100\n", pid, pid, p.ppid, p.utime, p.cutime, p.start)
	h.fs[fmt.Sprintf("proc/%d/stat", pid)] = &fstest.MapFile{Data: []byte(line)}
}

func (h host) kill(pid int) { delete(h.fs, fmt.Sprintf("proc/%d/stat", pid)) }

func (h host) snapshot(t *testing.T) procfs.Snapshot {
	t.Helper()
	s, err := procfs.TakeSnapshot(h.fs)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestGroupsFollowTheirTrees(t *testing.T) {
	h := host{fs: fstest.MapFS{}}
	h.set(1, pstat{start: 1})
	h.set(100, pstat{ppid: 1, start: 10, utime: 500})   // ollama serve
	h.set(101, pstat{ppid: 100, start: 11, utime: 900}) // its model runner
	h.set(200, pstat{ppid: 1, start: 20, utime: 50})    // llama-server
	h.set(300, pstat{ppid: 1, start: 30, utime: 7000})  // something else
	tr := New(h.fs)
	tr.Refresh(h.snapshot(t), map[string][]int{"ollama-11434": {100}, "llamacpp-8080": {200}})
	if g := tr.Groups(); g["ollama-11434"] != 2 || g["llamacpp-8080"] != 1 || len(g) != 2 {
		t.Fatalf("groups %v", g)
	}
	// Time accumulated before tracking is not counted.
	if got := tr.Sample(); len(got) != 0 {
		t.Fatalf("first sample %v", got)
	}
	h.set(100, pstat{ppid: 1, start: 10, utime: 510})
	h.set(101, pstat{ppid: 100, start: 11, utime: 990})
	h.set(200, pstat{ppid: 1, start: 20, utime: 55})
	h.set(300, pstat{ppid: 1, start: 30, utime: 9000})
	got := tr.Sample()
	if got["ollama-11434"] != 100 || got["llamacpp-8080"] != 5 || len(got) != 2 {
		t.Fatalf("sample %v", got)
	}
}

func TestReapedChildIsNotCountedTwice(t *testing.T) {
	h := host{fs: fstest.MapFS{}}
	h.set(100, pstat{ppid: 1, start: 10, utime: 500})
	h.set(101, pstat{ppid: 100, start: 11, utime: 900})
	tr := New(h.fs)
	tr.Refresh(h.snapshot(t), map[string][]int{"b": {100}})
	h.set(101, pstat{ppid: 100, start: 11, utime: 950})
	if got := tr.Sample()["b"]; got != 50 {
		t.Fatalf("child ran 50 ticks, got %d", got)
	}
	// The child runs 20 more ticks and exits; the parent reaps it, so its
	// whole time, 970 ticks, moves into the parent's reaped-child counter.
	h.kill(101)
	h.set(100, pstat{ppid: 1, start: 10, utime: 503, cutime: 970})
	if got := tr.Sample()["b"]; got != 23 {
		t.Fatalf("parent 3 + child's last 20 ticks, got %d", got)
	}
	if g := tr.Groups()["b"]; g != 1 {
		t.Fatalf("tracked %d", g)
	}
}

func TestChildReapedBetweenSampleAndRefresh(t *testing.T) {
	h := host{fs: fstest.MapFS{}}
	h.set(100, pstat{ppid: 1, start: 10, utime: 500})
	h.set(101, pstat{ppid: 100, start: 11, utime: 900})
	tr := New(h.fs)
	tr.Refresh(h.snapshot(t), map[string][]int{"b": {100}})
	tr.Sample()
	h.kill(101)
	h.set(100, pstat{ppid: 1, start: 10, utime: 500, cutime: 910})
	// A new child appears with the refresh; its earlier time is not counted.
	h.set(102, pstat{ppid: 100, start: 40, utime: 300})
	tr.Refresh(h.snapshot(t), map[string][]int{"b": {100}})
	h.set(102, pstat{ppid: 100, start: 40, utime: 301})
	if got := tr.Sample()["b"]; got != 11 {
		t.Fatalf("child's last 10 ticks + 1 of the new one, got %d", got)
	}
}

func TestPIDReuseAndAutoReap(t *testing.T) {
	h := host{fs: fstest.MapFS{}}
	h.set(100, pstat{ppid: 1, start: 10, utime: 500})
	h.set(101, pstat{ppid: 100, start: 11, utime: 900})
	tr := New(h.fs)
	tr.Refresh(h.snapshot(t), map[string][]int{"b": {100}})
	// The child exits and the kernel reaps it without crediting the parent;
	// the parent's counter does not include it.
	h.kill(101)
	h.set(100, pstat{ppid: 1, start: 10, utime: 505})
	if got := tr.Sample()["b"]; got != 0 {
		t.Fatalf("got %d", got)
	}
	h.set(100, pstat{ppid: 1, start: 10, utime: 512})
	if got := tr.Sample()["b"]; got != 7 {
		t.Fatalf("counting resumes: got %d", got)
	}
	// The root is replaced by another process with the same PID.
	h.set(100, pstat{ppid: 1, start: 99, utime: 4000})
	if got := tr.Sample()["b"]; got != 0 || tr.Groups()["b"] != 0 {
		t.Fatalf("a reused PID is a different process: %d, %v", got, tr.Groups())
	}
}

func TestProcessUnderTwoRoots(t *testing.T) {
	h := host{fs: fstest.MapFS{}}
	h.set(100, pstat{ppid: 1, start: 10})
	h.set(101, pstat{ppid: 100, start: 11})
	tr := New(h.fs)
	tr.Refresh(h.snapshot(t), map[string][]int{"b": {100}, "a": {101}})
	if g := tr.Groups(); g["a"] != 1 || g["b"] != 1 {
		t.Fatalf("groups %v", g)
	}
}
