// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package store

import (
	"context"
	"testing"
	"time"
)

func TestContextEvents(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	t0 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	for i, e := range []struct{ kind, subject, value, previous string }{
		{ContextKernel, "", "7.0.0", ""},
		{ContextBackendVersion, "ollama", "0.40.6", ""},
		{ContextBackendVersion, "ollama", "0.41.0", "0.40.6"},
		{ContextModelDigest, "ollama/qwen", "a8b0", ""},
	} {
		if err := s.RecordContext(ctx, t0.Add(time.Duration(i)*time.Hour), e.kind, e.subject, e.value, e.previous); err != nil {
			t.Fatal(err)
		}
	}
	last, err := s.LatestContext(ctx)
	if err != nil || last[ContextKey(ContextBackendVersion, "ollama")] != "0.41.0" || last[ContextKey(ContextKernel, "")] != "7.0.0" || len(last) != 3 {
		t.Fatalf("latest %v %v", last, err)
	}
	evs, err := s.ContextEvents(ctx, t0.Add(time.Hour), t0.Add(3*time.Hour))
	if err != nil || len(evs) != 2 || evs[1].Value != "0.41.0" || evs[1].Previous != "0.40.6" || !evs[0].At.Equal(t0.Add(time.Hour)) {
		t.Fatalf("events %+v %v", evs, err)
	}
}
