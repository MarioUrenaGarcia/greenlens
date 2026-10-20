// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package client talks to the agent over its Unix socket.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/bench"
	"github.com/MarioUrenaGarcia/greenlens/internal/calibrate"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
)

// Client is a connection to one agent.
type Client struct {
	// Socket is the path of the agent socket.
	Socket string
	http   *http.Client
}

// New returns a client for the agent listening on socket.
func New(socket string) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}
	return &Client{Socket: socket, http: &http.Client{Transport: tr, Timeout: 30 * time.Second}}
}

// Connect returns a client for the first socket of the list that answers.
// When none does, the error names the last one tried.
func Connect(ctx context.Context, sockets []string) (*Client, error) {
	var last error
	tried := ""
	for _, s := range sockets {
		c := New(s)
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := c.get(ctx, api.PathHealth, nil, nil)
		cancel()
		if err == nil {
			return c, nil
		}
		last, tried = err, s
	}
	detail := "no socket"
	if last != nil {
		detail = last.Error()
	}
	return nil, help.New(help.GL4001, "socket", tried, "detail", detail).Wrap(last)
}

func (c *Client) get(ctx context.Context, path string, q url.Values, v any) error {
	u := url.URL{Scheme: "http", Host: "agent", Path: path, RawQuery: q.Encode()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	return c.do(req, v)
}

func (c *Client) do(req *http.Request, v any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e api.ErrorBody
		detail := string(bytes.TrimSpace(body))
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			detail = e.Error
		}
		return help.New(help.GL4006, "status", resp.Status, "detail", detail)
	}
	if v == nil {
		return nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("decode %s: %w", req.URL.Path, err)
	}
	return nil
}

// Status returns the state of the agent.
func (c *Client) Status(ctx context.Context) (api.Status, error) {
	var s api.Status
	err := c.get(ctx, api.PathStatus, nil, &s)
	return s, err
}

// Usage returns the node energy in [from, to), in buckets of the given size
// or as one total when bucket is zero.
func (c *Client) Usage(ctx context.Context, from, to time.Time, bucket time.Duration) (api.Usage, error) {
	// Nanoseconds keep the last second of the period, which a truncated
	// time would drop.
	q := url.Values{"from": {from.UTC().Format(time.RFC3339Nano)}, "to": {to.UTC().Format(time.RFC3339Nano)}}
	if bucket > 0 {
		q.Set("bucket", bucket.String())
	}
	var u api.Usage
	err := c.get(ctx, api.PathUsage, q, &u)
	return u, err
}

// BackendUsage returns the usage of a period by backend or by model.
func (c *Client) BackendUsage(ctx context.Context, from, to time.Time, groupBy string) (api.BackendUsage, error) {
	q := url.Values{"from": {from.UTC().Format(time.RFC3339Nano)}, "to": {to.UTC().Format(time.RFC3339Nano)}, "group_by": {groupBy}}
	var u api.BackendUsage
	err := c.get(ctx, api.PathUsage, q, &u)
	return u, err
}

// SaveBenchmark stores a benchmark in the agent and returns it with its id.
func (c *Client) SaveBenchmark(ctx context.Context, b bench.Result) (bench.Result, error) {
	body, err := json.Marshal(b)
	if err != nil {
		return b, err
	}
	u := url.URL{Scheme: "http", Host: "agent", Path: api.PathBenchmarks}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return b, err
	}
	req.Header.Set("Content-Type", "application/json")
	var out bench.Result
	err = c.do(req, &out)
	return out, err
}

// Benchmark returns one stored benchmark.
func (c *Client) Benchmark(ctx context.Context, id int64) (bench.Result, error) {
	var b bench.Result
	err := c.get(ctx, api.PathBenchmarks+"/"+strconv.FormatInt(id, 10), nil, &b)
	return b, err
}

// Benchmarks lists the latest stored benchmarks.
func (c *Client) Benchmarks(ctx context.Context, limit int) (api.Benchmarks, error) {
	var b api.Benchmarks
	err := c.get(ctx, api.PathBenchmarks, url.Values{"limit": {strconv.Itoa(limit)}}, &b)
	return b, err
}

// Backends returns the backends of the node.
func (c *Client) Backends(ctx context.Context) (api.Backends, error) {
	var b api.Backends
	err := c.get(ctx, api.PathBackends, nil, &b)
	return b, err
}

// Live returns the samples the agent keeps in memory.
func (c *Client) Live(ctx context.Context) (api.Live, error) {
	var l api.Live
	err := c.get(ctx, api.PathLive, nil, &l)
	return l, err
}

// SaveCalibration stores a calibration in the agent, which applies it at
// once.
func (c *Client) SaveCalibration(ctx context.Context, r calibrate.Result) error {
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	u := url.URL{Scheme: "http", Host: "agent", Path: api.PathCalibrations}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, nil)
}

// IsUnreachable reports whether err means the agent could not be reached.
func IsUnreachable(err error) bool {
	var e *help.Error
	return errors.As(err, &e) && e.Code == help.GL4001
}
