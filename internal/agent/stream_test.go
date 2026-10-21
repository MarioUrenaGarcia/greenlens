// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
)

type sse struct {
	id         uint64
	name, data string
}

// readEvents reads server-sent events until stop returns true or the
// stream ends. Comments are returned as events named ":".
func readEvents(t *testing.T, body io.Reader, stop func(sse) bool) []sse {
	t.Helper()
	var out []sse
	var cur sse
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if cur.name != "" || cur.data != "" {
				out = append(out, cur)
				if stop(cur) {
					return out
				}
			}
			cur = sse{}
		case strings.HasPrefix(line, ":"):
			cur.name = ":"
		case strings.HasPrefix(line, "id: "):
			cur.id, _ = strconv.ParseUint(strings.TrimPrefix(line, "id: "), 10, 64)
		case strings.HasPrefix(line, "event: "):
			cur.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		}
	}
	return out
}

func openStream(t *testing.T, r *running, lastID uint64) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	req, err := http.NewRequestWithContext(ctx, "GET", "http://agent"+api.PathStream, nil)
	if err != nil {
		t.Fatal(err)
	}
	if lastID > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatUint(lastID, 10))
	}
	resp, err := socketClient(r.socket).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %d %v", resp.StatusCode, resp.Header)
	}
	return resp, cancel
}

func TestStream(t *testing.T) {
	r := startAgent(t, "")
	waitSamples(t, r, 5)
	spec := loadSpec(t)
	point := map[string]any{"$ref": "#/components/schemas/LivePoint"}
	resp, cancel := openStream(t, r, 0)
	// The samples in memory come first, then the backends, then new samples.
	var sawBackends bool
	evs := readEvents(t, resp.Body, func(e sse) bool {
		sawBackends = sawBackends || e.name == eventBackends
		return sawBackends && e.name == eventSample
	})
	cancel()
	_ = resp.Body.Close()
	var ids []uint64
	for _, e := range evs {
		switch e.name {
		case eventSample:
			if err := spec.Validate(point, []byte(e.data)); err != nil {
				t.Fatalf("sample: %v %s", err, e.data)
			}
			ids = append(ids, e.id)
		case eventBackends:
			if err := spec.Validate(map[string]any{"$ref": "#/components/schemas/Backends"}, []byte(e.data)); err != nil {
				t.Fatalf("backends: %v %s", err, e.data)
			}
		}
	}
	if len(ids) < 5 {
		t.Fatalf("only %d samples: %+v", len(ids), evs)
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] != ids[i-1]+1 {
			t.Fatalf("ids are not consecutive: %v", ids)
		}
	}

	// A client that reconnects gets only what it missed.
	from := ids[len(ids)-3]
	resp, cancel = openStream(t, r, from)
	evs = readEvents(t, resp.Body, func(e sse) bool { return e.name == eventSample })
	cancel()
	_ = resp.Body.Close()
	if len(evs) != 1 || evs[0].id != from+1 {
		t.Fatalf("resumed at %+v, want id %d", evs, from+1)
	}
}

func TestStreamDropsSlowClients(t *testing.T) {
	s := newStream()
	slow, ok := s.subscribe()
	if !ok {
		t.Fatal("no subscription")
	}
	for i := range streamBuffer + 1 {
		s.publish(event{id: uint64(i + 1), name: eventSample})
	}
	n := 0
	for range slow {
		n++
	}
	if n != streamBuffer {
		t.Fatalf("got %d events before the drop", n)
	}
	if s.listening() {
		t.Fatal("a dropped client is still listening")
	}
	var chans []chan event
	for range maxStreamClients {
		ch, ok := s.subscribe()
		if !ok {
			t.Fatal("refused below the limit")
		}
		chans = append(chans, ch)
	}
	if _, ok := s.subscribe(); ok {
		t.Fatal("accepted beyond the limit")
	}
	s.unsubscribe(chans[0])
	s.unsubscribe(chans[0])
	if _, ok := s.subscribe(); !ok {
		t.Fatal("a freed place was not reused")
	}
}

func TestStreamEndsOnShutdown(t *testing.T) {
	r := startAgent(t, "")
	waitSamples(t, r, 2)
	resp, cancel := openStream(t, r, 0)
	defer cancel()
	defer func() { _ = resp.Body.Close() }()
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		close(done)
	}()
	start := time.Now()
	r.stop(t)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("an open stream held up the shutdown for %s", d)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("the stream stayed open after the shutdown")
	}
}
