// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/sampler"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

// Limits of the live stream.
const (
	maxStreamClients = 32
	// streamBuffer is how many events a client may fall behind before it
	// is dropped; it reconnects and resumes from the samples in memory.
	streamBuffer = 64
	keepAlive    = 15 * time.Second
)

// Names of the events of the live stream.
const (
	eventSample   = "sample"
	eventBackends = "backends"
)

type event struct {
	id   uint64
	name string
	data []byte
}

// stream fans the live events out to the clients of PathStream.
type stream struct {
	mu   sync.Mutex
	subs map[chan event]struct{}
}

func newStream() *stream { return &stream{subs: map[chan event]struct{}{}} }

func (s *stream) subscribe() (chan event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.subs) >= maxStreamClients {
		return nil, false
	}
	ch := make(chan event, streamBuffer)
	s.subs[ch] = struct{}{}
	return ch, true
}

func (s *stream) unsubscribe(ch chan event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.subs[ch]; ok {
		delete(s.subs, ch)
		close(ch)
	}
}

func (s *stream) listening() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs) > 0
}

// publish never blocks the sampling loop: a client whose buffer is full is
// disconnected instead.
func (s *stream) publish(ev event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- ev:
		default:
			delete(s.subs, ch)
			close(ch)
		}
	}
}

func livePoint(s sampler.Sample) api.LivePoint {
	p := api.LivePoint{Time: s.Time, IntervalMS: s.Interval.Milliseconds(), PowerW: map[source.Domain]float64{}}
	for _, c := range s.Channels {
		p.PowerW[c.Domain] += c.PowerW
	}
	return p
}

// publishSample sends a new sample to the live clients.
func (a *Agent) publishSample(seq uint64, s sampler.Sample) {
	if !a.stream.listening() {
		return
	}
	data, err := json.Marshal(livePoint(s))
	if err != nil {
		return
	}
	a.stream.publish(event{id: seq, name: eventSample, data: data})
}

// publishBackends sends the backends after each usage interval.
func (a *Agent) publishBackends() {
	if !a.stream.listening() {
		return
	}
	data, err := json.Marshal(a.Backends())
	if err != nil {
		return
	}
	a.stream.publish(event{name: eventBackends, data: data})
}

func writeEvent(w io.Writer, ev event) error {
	var err error
	if ev.id > 0 {
		_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.id, ev.name, ev.data)
	} else {
		_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.name, ev.data)
	}
	return err
}

// handleStream serves the live power of the node and the use of the
// backends as server-sent events. A client that reconnects with
// Last-Event-ID receives the samples it missed, as long as they are still
// in memory.
func (a *Agent) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}
	ch, ok := a.stream.subscribe()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "too many live clients")
		return
	}
	defer a.stream.unsubscribe(ch)
	// A stream lasts longer than the write timeout of the server.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		a.limited("stream", "the write deadline of a live client cannot be lifted", "error", err)
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := io.WriteString(w, "retry: 3000\n\n"); err != nil {
		return
	}

	last, _ := strconv.ParseUint(r.Header.Get("Last-Event-ID"), 10, 64)
	a.mu.RLock()
	samples, n := a.ring.all(), a.samples
	a.mu.RUnlock()
	first := n - uint64(len(samples)) + 1
	for i, s := range samples {
		seq := first + uint64(i)
		if seq <= last {
			continue
		}
		data, err := json.Marshal(livePoint(s))
		if err != nil {
			return
		}
		if writeEvent(w, event{id: seq, name: eventSample, data: data}) != nil {
			return
		}
		last = seq
	}
	if data, err := json.Marshal(a.Backends()); err == nil {
		if writeEvent(w, event{name: eventBackends, data: data}) != nil {
			return
		}
	}
	flusher.Flush()

	tick := time.NewTicker(keepAlive)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-a.stopping:
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			// Samples sent from memory above may arrive again.
			if ev.id > 0 && ev.id <= last {
				continue
			}
			if writeEvent(w, ev) != nil {
				return
			}
			if ev.id > 0 {
				last = ev.id
			}
			flusher.Flush()
		case <-tick.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
