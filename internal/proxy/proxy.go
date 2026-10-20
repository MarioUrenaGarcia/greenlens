// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package proxy is a reverse proxy for inference servers that do not count
// tokens, such as Ollama and other OpenAI-compatible servers. It forwards
// every request and response unchanged, streaming included, and reads only
// the token counts and the model name of the responses as they pass. It
// never reads request bodies, and never stores or logs the content of
// either; consumer keys are kept only as HMACs.
package proxy

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/connector"
)

// EnergyHeader carries the energy of a request, in joules.
const EnergyHeader = "X-GreenLens-Energy-Joules"

// maxLine bounds a line of a streamed response that is parsed; longer
// lines pass through uncounted.
const maxLine = 1 << 20

// maxBuffered bounds a response held back to add the energy header to it.
const maxBuffered = 8 << 20

// Options configure a proxy.
type Options struct {
	Upstream *url.URL
	// Backend returns the name of the backend the upstream belongs to.
	Backend func() string
	Meter   *Meter
	// EnergyHeader adds EnergyHeader to every response.
	EnergyHeader bool
	// ConsumerHeader names the header that identifies consumers.
	ConsumerHeader string
	Consumers      *Consumers
	Log            *slog.Logger
}

type key struct {
	backend, model, consumer string
}

// Proxy forwards requests to one upstream and counts their tokens.
type Proxy struct {
	o  Options
	rp *httputil.ReverseProxy

	mu       sync.Mutex
	counters map[key]*connector.Counters
	requests uint64
}

type ctxKey struct{}

// state follows one request through the proxy.
type state struct {
	backend  string
	consumer string
	req      *Request
}

// New returns a proxy.
func New(o Options) *Proxy {
	p := &Proxy{o: o, counters: map[key]*connector.Counters{}}
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			// The upstream sees its own host name, as servers that guard
			// against DNS rebinding expect, and the client address in
			// X-Forwarded-For.
			r.SetURL(o.Upstream)
			r.SetXForwarded()
		},
		// Streamed responses are passed on as they arrive.
		FlushInterval:  -1,
		ModifyResponse: p.modify,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if st, ok := r.Context().Value(ctxKey{}).(*state); ok && st.req != nil {
				o.Meter.End(st.req)
			}
			// The error says how the upstream failed, never what was sent.
			o.Log.Warn("the proxy could not reach the upstream", "upstream", o.Upstream.Redacted(), "error", err)
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	return p
}

// ServeHTTP implements http.Handler.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	st := &state{backend: p.o.Backend()}
	if p.o.ConsumerHeader != "" {
		st.consumer = p.o.Consumers.Name(r.Header.Get(p.o.ConsumerHeader))
	}
	st.req = p.o.Meter.Begin(st.backend)
	p.rp.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, st)))
}

// streamed reports whether a response arrives in pieces: server-sent
// events, newline-delimited JSON, or a body of unknown length.
func streamed(resp *http.Response) bool {
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch mt {
	case "text/event-stream", "application/x-ndjson":
		return true
	}
	return resp.ContentLength < 0 && mt != "application/json"
}

func (p *Proxy) modify(resp *http.Response) error {
	st, ok := resp.Request.Context().Value(ctxKey{}).(*state)
	if !ok {
		return nil
	}
	t := &tally{}
	if !streamed(resp) && p.o.EnergyHeader {
		// The header goes before the body, so the body is read first; the
		// upstream has finished by then and the energy is known.
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBuffered+1))
		_ = resp.Body.Close()
		if err != nil {
			p.o.Meter.End(st.req)
			return err
		}
		if len(body) <= maxBuffered {
			t.line(body)
		}
		e := p.o.Meter.End(st.req)
		p.record(st, t)
		resp.Header.Set(EnergyHeader, strconv.FormatFloat(e, 'f', 3, 64))
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
		return nil
	}
	if p.o.EnergyHeader {
		// Streamed responses carry the energy as a trailer.
		if resp.Trailer == nil {
			resp.Trailer = http.Header{}
		}
		resp.Trailer[http.CanonicalHeaderKey(EnergyHeader)] = nil
	}
	resp.Body = &counter{Reader: resp.Body, closer: resp.Body, p: p, st: st, t: t, resp: resp, stream: streamed(resp)}
	return nil
}

// record adds the counts of a finished response.
func (p *Proxy) record(st *state, t *tally) {
	if !t.seen {
		return
	}
	prompt, gen := t.counts()
	k := key{backend: st.backend, model: t.model, consumer: st.consumer}
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.counters[k]
	if !ok {
		c = &connector.Counters{Model: t.model, Consumer: st.consumer}
		p.counters[k] = c
	}
	c.PromptTokens += float64(prompt)
	c.GeneratedTokens += float64(gen)
	c.Requests++
	p.requests++
}

// Counters returns the cumulative counts of a backend by model and
// consumer, in the form of a connector snapshot.
func (p *Proxy) Counters(backend string) []connector.Counters {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []connector.Counters
	for k, c := range p.counters {
		if k.backend == backend {
			out = append(out, *c)
		}
	}
	slices.SortFunc(out, func(a, b connector.Counters) int {
		if c := strings.Compare(a.Model, b.Model); c != 0 {
			return c
		}
		return strings.Compare(a.Consumer, b.Consumer)
	})
	return out
}

// Requests returns the number of responses with token counts seen.
func (p *Proxy) Requests() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests
}

// counter passes a response body through and reads its token counts line
// by line, keeping at most one line in memory.
type counter struct {
	io.Reader
	closer io.Closer
	p      *Proxy
	st     *state
	t      *tally
	resp   *http.Response
	stream bool
	line   []byte
	long   bool
	once   sync.Once
}

func (c *counter) Read(b []byte) (int, error) {
	n, err := c.Reader.Read(b)
	c.scan(b[:n])
	if err != nil {
		c.finish()
	}
	return n, err
}

func (c *counter) scan(b []byte) {
	if !c.stream {
		// A whole document, which may span lines, is parsed at the end.
		if !c.long {
			if len(c.line)+len(b) > maxBuffered {
				c.long, c.line = true, nil
			} else {
				c.line = append(c.line, b...)
			}
		}
		return
	}
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		chunk := b
		if i >= 0 {
			chunk = b[:i]
		}
		if !c.long {
			if len(c.line)+len(chunk) > maxLine {
				c.long, c.line = true, c.line[:0]
			} else {
				c.line = append(c.line, chunk...)
			}
		}
		if i < 0 {
			return
		}
		c.flushLine()
		b = b[i+1:]
	}
}

func (c *counter) flushLine() {
	if !c.long && len(c.line) > 0 && c.t.line(c.line) {
		c.p.o.Meter.Piece(c.st.req)
	}
	c.line, c.long = c.line[:0], false
}

// finish counts the last line and ends the request once.
func (c *counter) finish() {
	c.once.Do(func() {
		c.flushLine()
		e := c.p.o.Meter.End(c.st.req)
		c.p.record(c.st, c.t)
		if c.resp.Trailer != nil {
			if _, ok := c.resp.Trailer[http.CanonicalHeaderKey(EnergyHeader)]; ok {
				c.resp.Trailer.Set(EnergyHeader, strconv.FormatFloat(e, 'f', 3, 64))
			}
		}
	})
}

// Close implements io.Closer; a client that goes away still ends the
// request.
func (c *counter) Close() error {
	c.finish()
	return c.closer.Close()
}

// NewServer returns the HTTP server of a proxy. Timeouts bound headers and
// idle connections only, since generating a response can take minutes.
func NewServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
}
