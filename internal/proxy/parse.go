// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package proxy

import (
	"bytes"
	"encoding/json"
)

// piece is what a response, or one line of a streamed response, says about
// tokens. The decoder fills only these fields and skips the text of the
// response without keeping it.
type piece struct {
	Model string `json:"model"`
	// Ollama: /api/generate and /api/chat (docs/api.md).
	Done            *bool  `json:"done"`
	PromptEvalCount *int64 `json:"prompt_eval_count"`
	EvalCount       *int64 `json:"eval_count"`
	// OpenAI-compatible: /v1/chat/completions and /v1/completions.
	Usage *struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
	Choices nonEmpty `json:"choices"`
}

// nonEmpty records whether a field holds a non-empty array, without
// keeping its elements, which carry the generated text.
type nonEmpty bool

// UnmarshalJSON implements json.Unmarshaler.
func (n *nonEmpty) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	*n = len(b) >= 2 && b[0] == '[' && len(bytes.TrimSpace(b[1:len(b)-1])) > 0
	return nil
}

// tally accumulates the token counts of one response.
type tally struct {
	model     string
	prompt    int64
	generated int64
	// exact is true once a final count was read; until then generated
	// counts the streamed pieces.
	exact  bool
	pieces int64
	seen   bool
}

// line reads one JSON document or one line of a stream and reports
// whether it was a piece of generated output.
func (t *tally) line(b []byte) bool {
	b = bytes.TrimSpace(b)
	// Server-sent events: "data: {...}", "data: [DONE]", comments and
	// other fields.
	if v, ok := bytes.CutPrefix(b, []byte("data:")); ok {
		b = bytes.TrimSpace(v)
	}
	if len(b) == 0 || b[0] != '{' {
		return false
	}
	var p piece
	if json.Unmarshal(b, &p) != nil {
		return false
	}
	if p.Model != "" {
		t.model = p.Model
	}
	switch {
	case p.Usage != nil:
		t.prompt, t.generated, t.exact, t.seen = p.Usage.PromptTokens, p.Usage.CompletionTokens, true, true
		return false
	case p.PromptEvalCount != nil || p.EvalCount != nil:
		if p.PromptEvalCount != nil {
			t.prompt = *p.PromptEvalCount
		}
		if p.EvalCount != nil {
			t.generated = *p.EvalCount
		}
		t.exact, t.seen = true, true
		return false
	case p.Done != nil && !*p.Done, bool(p.Choices):
		t.pieces++
		t.seen = true
		return true
	}
	return false
}

// counts returns the tokens of the response: the final counts when the
// server sent them, otherwise the streamed pieces as generated tokens.
func (t *tally) counts() (prompt, generated int64) {
	if t.exact {
		return t.prompt, t.generated
	}
	return 0, t.pieces
}
