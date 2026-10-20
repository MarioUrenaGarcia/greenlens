// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package discovery

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

func TestMatch(t *testing.T) {
	tests := []struct {
		argv string
		kind Kind
		ok   bool
	}{
		{"/usr/local/bin/ollama serve", Ollama, true},
		{"/usr/local/bin/ollama runner --model x", Ollama, false},
		{"/opt/llama.cpp/build/bin/llama-server -m q.gguf --port 8081", LlamaCpp, true},
		{"text-generation-launcher --model-id Qwen/Qwen2.5-0.5B", TGI, true},
		{"/home/u/venv/bin/python3 /home/u/venv/bin/vllm serve Qwen/Qwen2.5-0.5B", VLLM, true},
		{"/home/u/venv/bin/vllm serve Qwen/Qwen2.5-0.5B", VLLM, true},
		{"python -m vllm.entrypoints.openai.api_server --model m", VLLM, true},
		{"python3 -m sglang.launch_server --model-path m --port 30001", SGLang, true},
		{"local-ai run", LocalAI, true},
		{"python3 train.py --vllm", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		k, ok := match(strings.Fields(tt.argv))
		if ok != tt.ok || (ok && k != tt.kind) {
			t.Errorf("match(%q) = %q %v", tt.argv, k, ok)
		}
	}
}

func TestPorts(t *testing.T) {
	if got := ports(LlamaCpp, strings.Fields("llama-server --port 8081")); !slices.Equal(got, []int{8081, 9931, 8080}) {
		t.Errorf("llama.cpp %v", got)
	}
	if got := ports(VLLM, strings.Fields("vllm serve m --port=9000")); !slices.Equal(got, []int{9000, 8000}) {
		t.Errorf("vllm %v", got)
	}
	if got := ports(Ollama, nil); !slices.Equal(got, []int{11434}) {
		t.Errorf("ollama %v", got)
	}
	if got := ports(TGI, strings.Fields("x --port 99999")); !slices.Equal(got, []int{3000}) {
		t.Errorf("invalid port kept: %v", got)
	}
}

func stat(pid, ppid int) []byte {
	return []byte(strings.Join([]string{strconv.Itoa(pid), "(p)", "S", strconv.Itoa(ppid), "0 0 0 -1 0 0 0 0 0 10 5 0 0 20 0 1 0 777 0 0"}, " "))
}

func TestScan(t *testing.T) {
	fsys := fstest.MapFS{
		// ollama serve with its runner child.
		"proc/100/stat":    {Data: stat(100, 1)},
		"proc/100/cmdline": {Data: []byte("/usr/local/bin/ollama\x00serve\x00")},
		"proc/101/stat":    {Data: stat(101, 100)},
		"proc/101/cmdline": {Data: []byte("/usr/local/bin/ollama\x00runner\x00")},
		// vLLM in a container, with a worker that repeats the module name.
		"proc/200/stat":    {Data: stat(200, 1)},
		"proc/200/cmdline": {Data: []byte("python3\x00-m\x00vllm.entrypoints.openai.api_server\x00--port\x008001\x00")},
		"proc/200/cgroup":  {Data: []byte("0::/system.slice/docker-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.scope\n")},
		"proc/201/stat":    {Data: stat(201, 200)},
		"proc/201/cmdline": {Data: []byte("python3\x00-m\x00vllm.entrypoints.openai.api_server\x00--worker\x00")},
		// An unrelated process.
		"proc/300/stat":    {Data: stat(300, 1)},
		"proc/300/cmdline": {Data: []byte("bash\x00")},
	}
	got, err := Scan(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("candidates %+v", got)
	}
	if got[0].Kind != Ollama || got[0].PID != 100 || got[0].Endpoints[0] != "http://127.0.0.1:11434" {
		t.Errorf("ollama %+v", got[0])
	}
	if got[1].Kind != VLLM || got[1].ContainerID != "0123456789ab" || got[1].Endpoints[0] != "http://127.0.0.1:8001" || got[1].StartTime != 777 {
		t.Errorf("vllm %+v", got[1])
	}
	if Name(VLLM, "http://127.0.0.1:8001") != "vllm-8001" {
		t.Error("Name")
	}
}

// TestThisMachine runs the scan on the real /proc. It cannot assert what
// runs here, only that the scan succeeds and every candidate is coherent.
func TestThisMachine(t *testing.T) {
	cands, err := Scan(os.DirFS("/"))
	if err != nil {
		t.Skip("no /proc:", err)
	}
	for _, c := range cands {
		if c.PID <= 0 || len(c.Endpoints) == 0 || !slices.Contains(Kinds, c.Kind) {
			t.Errorf("incoherent candidate %+v", c)
		}
		t.Logf("found %s pid %d endpoints %v", c.Kind, c.PID, c.Endpoints)
	}
}
