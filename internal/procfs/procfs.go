// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package procfs parses the parts of /proc that GreenLens uses: system CPU
// time, per-process CPU time and process ancestry. Times are in clock ticks
// (USER_HZ), which the kernel fixes at 100 on every architecture GreenLens
// supports; only ratios of times are used, so the unit cancels out.
package procfs

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

// CPUTimes are the counters of the aggregate "cpu" line of /proc/stat.
// Guest time is already included in user and nice, so it is not read.
type CPUTimes struct {
	User, Nice, System, Idle, IOWait, IRQ, SoftIRQ, Steal uint64
}

// Total is the time accounted on all CPUs, steal included.
func (t CPUTimes) Total() uint64 {
	return t.User + t.Nice + t.System + t.Idle + t.IOWait + t.IRQ + t.SoftIRQ + t.Steal
}

// Busy is the time the CPUs spent running work of this system. Steal is
// time taken by the hypervisor for other guests, so it is not busy time of
// this machine.
func (t CPUTimes) Busy() uint64 {
	return t.User + t.Nice + t.System + t.IRQ + t.SoftIRQ
}

// SystemCPU reads the aggregate CPU times from proc/stat.
func SystemCPU(fsys fs.FS) (CPUTimes, error) {
	data, err := fs.ReadFile(fsys, "proc/stat")
	if err != nil {
		return CPUTimes{}, err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 || fields[0] != "cpu" {
			continue
		}
		if len(fields) < 9 {
			return CPUTimes{}, fmt.Errorf("proc/stat: short cpu line")
		}
		var v [8]uint64
		for i := range v {
			v[i], err = strconv.ParseUint(fields[i+1], 10, 64)
			if err != nil {
				return CPUTimes{}, fmt.Errorf("proc/stat: %w", err)
			}
		}
		return CPUTimes{v[0], v[1], v[2], v[3], v[4], v[5], v[6], v[7]}, nil
	}
	return CPUTimes{}, errors.New("proc/stat: no cpu line")
}

// ProcStat holds the fields of /proc/<pid>/stat that GreenLens uses.
type ProcStat struct {
	PID   int
	Comm  string
	State byte
	PPID  int
	// UTime and STime are the CPU time of the process itself; CUTime and
	// CSTime the time of its children that it has already waited for.
	UTime, STime, CUTime, CSTime uint64
	// StartTime is when the process started, in ticks after boot. Together
	// with the PID it identifies a process across PID reuse.
	StartTime uint64
}

// CPU is the time of the process and of its reaped children.
func (p ProcStat) CPU() uint64 { return p.UTime + p.STime + p.CUTime + p.CSTime }

// Process reads /proc/<pid>/stat.
func Process(fsys fs.FS, pid int) (ProcStat, error) {
	data, err := fs.ReadFile(fsys, "proc/"+strconv.Itoa(pid)+"/stat")
	if err != nil {
		return ProcStat{}, err
	}
	return ParseStat(data)
}

// ParseStat parses the content of /proc/<pid>/stat. The command name is in
// parentheses and may itself contain spaces and parentheses, so fields are
// counted from the last closing parenthesis.
func ParseStat(data []byte) (ProcStat, error) {
	s := strings.TrimSpace(string(data))
	open := strings.IndexByte(s, '(')
	end := strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		return ProcStat{}, errors.New("stat: malformed command name")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(s[:open]))
	if err != nil {
		return ProcStat{}, fmt.Errorf("stat: pid: %w", err)
	}
	rest := strings.Fields(s[end+1:])
	// rest[0] is field 3 (state); utime is field 14 and starttime field 22.
	if len(rest) < 20 {
		return ProcStat{}, errors.New("stat: too few fields")
	}
	p := ProcStat{PID: pid, Comm: s[open+1 : end]}
	if len(rest[0]) != 1 {
		return ProcStat{}, errors.New("stat: invalid state")
	}
	p.State = rest[0][0]
	if p.PPID, err = strconv.Atoi(rest[1]); err != nil {
		return ProcStat{}, fmt.Errorf("stat: ppid: %w", err)
	}
	nums := []*uint64{&p.UTime, &p.STime}
	for i, dst := range nums {
		if *dst, err = strconv.ParseUint(rest[11+i], 10, 64); err != nil {
			return ProcStat{}, fmt.Errorf("stat: %w", err)
		}
	}
	// cutime and cstime are signed in the kernel ABI; negative values do
	// not occur in practice and are treated as zero.
	for i, dst := range []*uint64{&p.CUTime, &p.CSTime} {
		v, err := strconv.ParseInt(rest[13+i], 10, 64)
		if err != nil {
			return ProcStat{}, fmt.Errorf("stat: %w", err)
		}
		if v > 0 {
			*dst = uint64(v)
		}
	}
	if p.StartTime, err = strconv.ParseUint(rest[19], 10, 64); err != nil {
		return ProcStat{}, fmt.Errorf("stat: starttime: %w", err)
	}
	return p, nil
}

// PIDs lists the processes in proc.
func PIDs(fsys fs.FS) ([]int, error) {
	entries, err := fs.ReadDir(fsys, "proc")
	if err != nil {
		return nil, err
	}
	var out []int
	for _, e := range entries {
		if pid, err := strconv.Atoi(e.Name()); err == nil && pid > 0 {
			out = append(out, pid)
		}
	}
	return out, nil
}

// Snapshot is the stat of every process at one moment.
type Snapshot map[int]ProcStat

// TakeSnapshot reads every process. Processes that exit while the snapshot
// is taken are skipped.
func TakeSnapshot(fsys fs.FS) (Snapshot, error) {
	pids, err := PIDs(fsys)
	if err != nil {
		return nil, err
	}
	snap := make(Snapshot, len(pids))
	for _, pid := range pids {
		p, err := Process(fsys, pid)
		if err != nil {
			continue
		}
		snap[pid] = p
	}
	return snap, nil
}

// TreeCPU returns the CPU time of root and of every live descendant,
// including the time of their reaped children. When a child exits and is
// reaped, its time moves from its own counters into the cutime and cstime
// of its parent, so the sum stays continuous. Descendants that are
// reparented away from the tree are no longer counted.
func (s Snapshot) TreeCPU(root int) (uint64, bool) {
	r, ok := s[root]
	if !ok {
		return 0, false
	}
	children := map[int][]int{}
	for pid, p := range s {
		children[p.PPID] = append(children[p.PPID], pid)
	}
	total := r.CPU()
	stack := append([]int(nil), children[root]...)
	seen := map[int]bool{root: true}
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		total += s[pid].CPU()
		stack = append(stack, children[pid]...)
	}
	return total, true
}
