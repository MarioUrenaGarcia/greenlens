// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package proctrack measures the CPU time of groups of processes, such as
// the processes of one inference backend, cheaply enough to run on every
// sample: the whole process tree is scanned only when the groups are
// refreshed, and in between only the stat files of the tracked processes
// are read.
package proctrack

import (
	"io/fs"
	"slices"

	"github.com/MarioUrenaGarcia/greenlens/internal/procfs"
)

type proc struct {
	group string
	start uint64
	ppid  int
	// cpu is the CPU time counted so far: its own and that of the children
	// it has reaped, as of the last reading.
	cpu uint64
}

// Tracker follows the processes of every group. It is not safe for
// concurrent use.
type Tracker struct {
	fsys  fs.FS
	procs map[int]*proc
}

// New returns a tracker that reads processes from fsys.
func New(fsys fs.FS) *Tracker {
	return &Tracker{fsys: fsys, procs: map[int]*proc{}}
}

// Refresh replaces the groups with the trees of their root processes in
// snap: every live descendant of a root joins the group of the root. A
// process under the roots of several groups belongs to the group that sorts
// first. Processes tracked before keep their counted time, so no CPU time
// is lost or counted twice across a refresh; new ones start from their
// time in snap.
func (t *Tracker) Refresh(snap procfs.Snapshot, roots map[string][]int) {
	children := map[int][]int{}
	for pid, p := range snap {
		children[p.PPID] = append(children[p.PPID], pid)
	}
	// Processes that ended since the last sample hand their time to the
	// parent that reaped them.
	t.settle(func(pid int) (procfs.ProcStat, bool) {
		p, ok := snap[pid]
		return p, ok
	})

	groups := make([]string, 0, len(roots))
	for g := range roots {
		groups = append(groups, g)
	}
	slices.Sort(groups)
	next := map[int]*proc{}
	for _, g := range groups {
		stack := slices.Clone(roots[g])
		for len(stack) > 0 {
			pid := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			st, ok := snap[pid]
			if !ok {
				continue
			}
			if _, done := next[pid]; done {
				continue
			}
			p := &proc{group: g, start: st.StartTime, ppid: st.PPID, cpu: st.CPU()}
			if old, ok := t.procs[pid]; ok && old.start == st.StartTime {
				p.cpu = old.cpu
			}
			next[pid] = p
			stack = append(stack, children[pid]...)
		}
	}
	t.procs = next
}

// settle removes the processes that no longer exist according to read and
// adds the time they had to the parent that reaped them, whose reaped-child
// counters now include it.
func (t *Tracker) settle(read func(pid int) (procfs.ProcStat, bool)) map[int]procfs.ProcStat {
	alive := map[int]procfs.ProcStat{}
	var gone []int
	for pid, p := range t.procs {
		st, ok := read(pid)
		if !ok || st.StartTime != p.start {
			gone = append(gone, pid)
			continue
		}
		alive[pid] = st
	}
	for _, pid := range gone {
		c := t.procs[pid]
		if parent, ok := t.procs[c.ppid]; ok {
			if _, live := alive[c.ppid]; live {
				parent.cpu += c.cpu
			}
		}
	}
	for _, pid := range gone {
		delete(t.procs, pid)
	}
	return alive
}

// Sample reads the tracked processes and returns the CPU time, in clock
// ticks, that each group used since the previous Sample or Refresh.
func (t *Tracker) Sample() map[string]uint64 {
	alive := t.settle(func(pid int) (procfs.ProcStat, bool) {
		st, err := procfs.Process(t.fsys, pid)
		return st, err == nil
	})
	out := map[string]uint64{}
	for pid, st := range alive {
		p := t.procs[pid]
		cur := st.CPU()
		// A parent credited with a child it did not collect, as when the
		// kernel reaps children automatically, would go negative; that
		// interval counts nothing for it and counting resumes from now.
		if cur > p.cpu {
			out[p.group] += cur - p.cpu
		}
		p.cpu = cur
		p.ppid = st.PPID
	}
	return out
}

// Groups returns the number of processes tracked in each group.
func (t *Tracker) Groups() map[string]int {
	out := map[string]int{}
	for _, p := range t.procs {
		out[p.group]++
	}
	return out
}

// GroupOf returns the group of a tracked process.
func (t *Tracker) GroupOf(pid int) (string, bool) {
	p, ok := t.procs[pid]
	if !ok {
		return "", false
	}
	return p.group, true
}
