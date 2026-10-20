// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package discovery finds the inference backends running on the machine:
// processes of known engines, the port each one listens on and the
// container it runs in. Backends declared in the configuration take
// precedence and keep their name across restarts.
package discovery

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/MarioUrenaGarcia/greenlens/internal/procfs"
)

// Kind is the engine of a backend.
type Kind string

// Kinds of backend.
const (
	Ollama   Kind = "ollama"
	LlamaCpp Kind = "llamacpp"
	VLLM     Kind = "vllm"
	TGI      Kind = "tgi"
	SGLang   Kind = "sglang"
	LocalAI  Kind = "localai"
	OpenAI   Kind = "openai"
	Process  Kind = "process"
)

// Kinds lists every kind accepted in the configuration.
var Kinds = []Kind{Ollama, LlamaCpp, VLLM, TGI, SGLang, LocalAI, OpenAI, Process}

// defaultPorts are the ports each engine listens on when no --port is
// given, from their sources: llama.cpp moved from 8080 to 9931.
var defaultPorts = map[Kind][]int{
	Ollama:   {11434},
	LlamaCpp: {9931, 8080},
	VLLM:     {8000},
	TGI:      {3000},
	SGLang:   {30000},
	LocalAI:  {8080},
}

// Backend is one inference server.
type Backend struct {
	Name        string `json:"name"`
	Kind        Kind   `json:"kind"`
	Endpoint    string `json:"endpoint,omitempty"`
	PID         int    `json:"pid,omitempty"`
	StartTime   uint64 `json:"-"`
	ContainerID string `json:"container_id,omitempty"`
	Static      bool   `json:"static"`
}

// Static is a backend declared in the configuration.
type Static struct {
	Name     string
	Kind     Kind
	Endpoint string
}

// match recognizes the root process of an engine from its command line.
func match(argv []string) (Kind, bool) {
	if len(argv) == 0 {
		return "", false
	}
	base := path.Base(argv[0])
	switch base {
	case "ollama":
		return Ollama, slices.Contains(argv[1:], "serve")
	case "llama-server":
		return LlamaCpp, true
	case "text-generation-launcher":
		return TGI, true
	case "local-ai":
		return LocalAI, true
	case "vllm":
		return VLLM, len(argv) > 1 && argv[1] == "serve"
	case "sglang":
		return SGLang, len(argv) > 1 && argv[1] == "serve"
	}
	if strings.HasPrefix(base, "python") {
		for i, a := range argv[1:] {
			if a != "-m" || i+2 >= len(argv) {
				continue
			}
			mod := argv[i+2]
			switch {
			case strings.HasPrefix(mod, "vllm.entrypoints"):
				return VLLM, true
			case strings.HasPrefix(mod, "sglang.launch_server"):
				return SGLang, true
			}
		}
		// Console scripts installed by pip run as "python .../bin/vllm serve".
		if len(argv) > 2 && path.Base(argv[1]) == "vllm" && argv[2] == "serve" {
			return VLLM, true
		}
	}
	return "", false
}

// Command line options that set the port.
var portFlag = regexp.MustCompile(`^--port(?:=(\d+))?$`)

// ports returns the ports to try for a process: the --port option, then
// the defaults of the engine.
func ports(kind Kind, argv []string) []int {
	var out []int
	for i, a := range argv {
		m := portFlag.FindStringSubmatch(a)
		if m == nil {
			continue
		}
		v := m[1]
		if v == "" && i+1 < len(argv) {
			v = argv[i+1]
		}
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p < 65536 {
			out = append(out, p)
		}
	}
	return append(out, defaultPorts[kind]...)
}

// containerID extracts the container of a process from its cgroup path,
// for Docker, containerd and Podman.
var containerPattern = regexp.MustCompile(`(?:docker-|cri-containerd-|libpod-|/docker/)([0-9a-f]{12,64})`)

func containerID(fsys fs.FS, pid int) string {
	data, err := fs.ReadFile(fsys, fmt.Sprintf("proc/%d/cgroup", pid))
	if err != nil {
		return ""
	}
	if m := containerPattern.FindSubmatch(data); m != nil {
		return string(m[1][:12])
	}
	return ""
}

func cmdline(fsys fs.FS, pid int) []string {
	data, err := fs.ReadFile(fsys, fmt.Sprintf("proc/%d/cmdline", pid))
	if err != nil || len(data) == 0 {
		return nil
	}
	parts := bytes.Split(bytes.TrimRight(data, "\x00"), []byte{0})
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = string(p)
	}
	return out
}

// Candidate is a recognized process with the endpoints to try.
type Candidate struct {
	Backend
	Endpoints []string
}

// Scan finds the root processes of known engines. A process whose ancestor
// is a backend of the same kind is part of that backend, not another one.
func Scan(fsys fs.FS) ([]Candidate, error) {
	snap, err := procfs.TakeSnapshot(fsys)
	if err != nil {
		return nil, err
	}
	kinds := map[int]Kind{}
	argvs := map[int][]string{}
	for pid := range snap {
		argv := cmdline(fsys, pid)
		if k, ok := match(argv); ok {
			kinds[pid] = k
			argvs[pid] = argv
		}
	}
	var out []Candidate
	for pid, k := range kinds {
		nested := false
		for p := snap[pid].PPID; p > 1; p = snap[p].PPID {
			if kinds[p] == k {
				nested = true
				break
			}
			if _, ok := snap[p]; !ok {
				break
			}
		}
		if nested {
			continue
		}
		c := Candidate{Backend: Backend{Kind: k, PID: pid, StartTime: snap[pid].StartTime, ContainerID: containerID(fsys, pid)}}
		for _, port := range ports(k, argvs[pid]) {
			c.Endpoints = append(c.Endpoints, fmt.Sprintf("http://127.0.0.1:%d", port))
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b Candidate) int { return a.PID - b.PID })
	return out, nil
}

// Name returns the name of a discovered backend: the kind and the port, so
// it stays the same when the process restarts with another PID.
func Name(kind Kind, endpoint string) string {
	if i := strings.LastIndexByte(endpoint, ':'); i >= 0 {
		return string(kind) + "-" + endpoint[i+1:]
	}
	return string(kind)
}
