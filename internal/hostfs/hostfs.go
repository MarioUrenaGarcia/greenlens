// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package hostfs reads the kernel interfaces under /sys and /proc through an
// fs.FS, so every parser can be tested against recorded trees of real
// hardware without that hardware.
package hostfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// Root returns the host file system rooted at dir. In production dir is "/";
// in a container it is the mount point of the host root, such as "/host".
// Paths passed to the functions of this package never start with a slash.
func Root(dir string) fs.FS {
	return os.DirFS(dir)
}

// String reads a sysfs or procfs attribute and trims the trailing newline.
func String(fsys fs.FS, name string) (string, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// Uint64 reads an attribute holding an unsigned decimal integer.
func Uint64(fsys fs.FS, name string) (uint64, error) {
	s, err := String(fsys, name)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return v, nil
}

// Int64 reads an attribute holding a signed decimal integer.
func Int64(fsys fs.FS, name string) (int64, error) {
	s, err := String(fsys, name)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return v, nil
}

// Exists reports whether name exists. Entries in /sys/class are symbolic
// links, so the check follows them.
func Exists(fsys fs.FS, name string) bool {
	_, err := fs.Stat(fsys, name)
	return err == nil
}

// Dirs lists the entries of dir that are directories or links to
// directories, in lexical order. A missing dir yields no entries.
func Dirs(fsys fs.FS, dir string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		info, err := fs.Stat(fsys, dir+"/"+e.Name())
		if err != nil || !info.IsDir() {
			continue
		}
		out = append(out, e.Name())
	}
	return out, nil
}

// IsPermission reports whether err comes from missing read permission.
func IsPermission(err error) bool {
	return errors.Is(err, fs.ErrPermission)
}

// CPUList parses the kernel cpulist format, such as "0-3,8,10-11", used by
// cpumask files and /sys/devices/system/cpu/online.
func CPUList(s string) ([]int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []int
	for _, part := range strings.Split(s, ",") {
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil || a < 0 {
			return nil, fmt.Errorf("invalid cpu list %q", s)
		}
		b := a
		if isRange {
			b, err = strconv.Atoi(hi)
			if err != nil || b < a {
				return nil, fmt.Errorf("invalid cpu list %q", s)
			}
		}
		for c := a; c <= b; c++ {
			out = append(out, c)
		}
	}
	return out, nil
}
