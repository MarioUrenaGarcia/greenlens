// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package promtext

import (
	"math"
	"strings"
	"testing"
)

const sample = `# HELP vllm:prompt_tokens_total Number of prefill tokens processed.
# TYPE vllm:prompt_tokens_total counter
vllm:prompt_tokens_total{engine="0",model_name="Qwen/Qwen2.5-0.5B"} 1234.0
vllm:generation_tokens_total{engine="0",model_name="Qwen/Qwen2.5-0.5B"} 5678
http_requests_total{method="post",code="200",path="/v1/completions"} 1027 1395066363000
weird{label="a \"quoted\" \\ value\nnext"} -Inf
bucket_le{le="+Inf"} 3 # {trace_id="abc"} 1.0
up 1
`

func TestParse(t *testing.T) {
	got, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 {
		t.Fatalf("got %d samples: %+v", len(got), got)
	}
	if got[0].Name != "vllm:prompt_tokens_total" || got[0].Labels["model_name"] != "Qwen/Qwen2.5-0.5B" || got[0].Value != 1234 {
		t.Errorf("first %+v", got[0])
	}
	if got[3].Labels["label"] != "a \"quoted\" \\ value\nnext" || !math.IsInf(got[3].Value, -1) {
		t.Errorf("escapes %+v", got[3])
	}
	if got[2].Value != 1027 || got[5].Labels != nil {
		t.Errorf("timestamp or bare sample %+v %+v", got[2], got[5])
	}
	if s := Sum(got, "vllm:generation_tokens_total", "model_name"); s["Qwen/Qwen2.5-0.5B"] != 5678 {
		t.Errorf("sum %v", s)
	}
	if f := Find(got, "http_requests_total", map[string]string{"code": "200"}); len(f) != 1 {
		t.Errorf("find %v", f)
	}
	if !Has(got, "up") || Has(got, "down") {
		t.Error("Has")
	}
}

func TestRejects(t *testing.T) {
	for _, bad := range []string{
		"9metric 1", "m{a=b} 1", `m{a="b} 1`, `m{a="b"`, "m", "m one", `m{a="\q"} 1`, "m 1 2 3",
	} {
		if _, err := Parse(strings.NewReader(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := Parse(strings.NewReader(strings.Repeat("x", MaxLineBytes+1))); err == nil {
		t.Error("over-long line accepted")
	}
}

func FuzzParse(f *testing.F) {
	f.Add(sample)
	f.Fuzz(func(t *testing.T, s string) {
		samples, err := Parse(strings.NewReader(s))
		if err != nil {
			return
		}
		for _, x := range samples {
			if x.Name == "" {
				t.Fatalf("empty name from %q", s)
			}
		}
	})
}
