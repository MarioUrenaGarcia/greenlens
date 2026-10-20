// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package backends

import (
	"fmt"
	"testing"
	"testing/fstest"

	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/procfs"
)

func proc(fsys fstest.MapFS, pid, ppid int, argv string) {
	fsys[fmt.Sprintf("proc/%d/stat", pid)] = &fstest.MapFile{Data: fmt.Appendf(nil, "%d (p) S %d 0 0 0 -1 0 0 0 0 0 1 1 0 0 20 0 1 0 %d 0 0", pid, ppid, pid)}
	fsys[fmt.Sprintf("proc/%d/cmdline", pid)] = &fstest.MapFile{Data: []byte(argv)}
}

func TestResolve(t *testing.T) {
	fsys := fstest.MapFS{}
	proc(fsys, 100, 1, "/usr/local/bin/ollama\x00serve\x00")
	proc(fsys, 101, 100, "/usr/local/lib/ollama/llama-server\x00--port\x0040355\x00")
	proc(fsys, 200, 1, "llama-server\x00--port\x008081\x00")
	proc(fsys, 300, 1, "llama-server\x00--port\x008082\x00")
	proc(fsys, 400, 1, "/usr/bin/python3\x00batch.py\x00")
	snap, err := procfs.TakeSnapshot(fsys)
	if err != nil {
		t.Fatal(err)
	}
	static := []config.Backend{
		{Name: "chat", Kind: "llamacpp", Endpoint: "http://127.0.0.1:8081"},
		{Name: "batch", Kind: "process", Process: "python3"},
		{Name: "absent", Kind: "vllm", Endpoint: "http://127.0.0.1:9000"},
	}
	got := map[string]Backend{}
	for _, b := range Resolve(fsys, snap, static, true) {
		got[b.Name] = b
	}
	if len(got) != 5 {
		t.Fatalf("backends %+v", got)
	}
	if b := got["chat"]; !b.Static || len(b.Roots) != 1 || b.Roots[0] != 200 {
		t.Errorf("declared by port: %+v", b)
	}
	if b := got["batch"]; len(b.Roots) != 1 || b.Roots[0] != 400 {
		t.Errorf("declared by executable: %+v", b)
	}
	if b := got["absent"]; len(b.Roots) != 0 || !b.Static {
		t.Errorf("a declared backend that is not running is kept: %+v", b)
	}
	// The llama-server that Ollama starts for a model is part of Ollama.
	if b := got["ollama-11434"]; len(b.Roots) != 1 || b.Roots[0] != 100 {
		t.Errorf("ollama %+v", b)
	}
	if _, ok := got["llamacpp-40355"]; ok {
		t.Error("the runner of Ollama became a backend")
	}
	// Without the socket tables every candidate port is kept; 8082 is the
	// first one of the second server.
	if b := got["llamacpp-8082"]; len(b.Roots) != 1 || b.Roots[0] != 300 {
		t.Errorf("discovered %+v", got)
	}
	roots := Roots(Resolve(fsys, snap, static, false))
	if len(roots) != 3 {
		t.Errorf("without discovery only the declared backends are kept: %v", roots)
	}
}
