// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/backends"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

// contextLog records the changes of what the measurements depend on,
// writing only when a value differs from the last one stored.
type contextLog struct {
	mu   sync.Mutex
	last map[string]string
}

// noteContext stores value as the current one of a subject when it changed.
func (a *Agent) noteContext(ctx context.Context, kind, subject, value string) {
	if value == "" || a.ctxLog == nil {
		return
	}
	key := store.ContextKey(kind, subject)
	a.ctxLog.mu.Lock()
	prev, seen := a.ctxLog.last[key]
	if seen && prev == value {
		a.ctxLog.mu.Unlock()
		return
	}
	a.ctxLog.last[key] = value
	a.ctxLog.mu.Unlock()
	if err := a.st.RecordContext(ctx, time.Now(), kind, subject, value, prev); err != nil {
		a.limited("context", "recording a change of context failed", "error", err)
	}
}

// startContext loads the last values and records those of the start: the
// agent, the kernel, the GPU drivers and the configuration.
func (a *Agent) startContext(ctx context.Context) {
	last, err := a.st.LatestContext(ctx)
	if err != nil {
		a.log.Warn("cannot load the context of the measurements", "error", err)
		last = map[string]string{}
	}
	a.ctxLog = &contextLog{last: last}
	a.noteContext(ctx, store.ContextAgentVersion, "", a.o.Version)
	if b, err := fs.ReadFile(a.hostFS, "proc/sys/kernel/osrelease"); err == nil {
		a.noteContext(ctx, store.ContextKernel, "", strings.TrimSpace(string(b)))
	}
	for _, c := range a.cands {
		if d, ok := c.Source.(interface{ Driver() string }); ok {
			a.noteContext(ctx, store.ContextGPUDriver, c.Source.ID(), d.Driver())
		}
	}
	a.noteConfig(ctx)
}

// noteConfig records a digest of the configuration file, so a change of
// it shows next to the measurements it affected.
func (a *Agent) noteConfig(ctx context.Context) {
	if a.o.ConfigPath == "" {
		return
	}
	b, err := os.ReadFile(a.o.ConfigPath)
	if err != nil {
		return
	}
	sum := sha256.Sum256(b)
	a.noteContext(ctx, store.ContextConfig, "", hex.EncodeToString(sum[:8]))
}

// noteBackends records the version of every backend that answered and the
// digest of each model it has loaded.
func (a *Agent) noteBackends(ctx context.Context, readings map[string]backends.Reading) {
	for name, rd := range readings {
		if rd.Err != nil {
			continue
		}
		a.noteContext(ctx, store.ContextBackendVersion, name, rd.Snapshot.Version)
		for model, digest := range rd.Snapshot.Digests {
			a.noteContext(ctx, store.ContextModelDigest, name+"/"+model, digest)
		}
	}
}
