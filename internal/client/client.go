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
		return help.New(help.GL4006, "status", resp.Status, "detail", detail).Wrap(&StatusError{Status: resp.StatusCode, Detail: detail})
	}
	if v == nil {
		return nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("decode %s: %w", req.URL.Path, err)
	}
	return nil
}

// StatusError is the answer of an agent that refused a request.
type StatusError struct {
	Status int
	Detail string
}

func (e *StatusError) Error() string { return strconv.Itoa(e.Status) + ": " + e.Detail }

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
	u := url.URL{Scheme: "http", Host: "agent", Path: api.PathBenchResults}
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

// Backup asks the agent for a consistent copy of its database and writes
// it to w. It returns the schema version the agent reported. The copy can
// take longer than other requests, so only ctx bounds it.
func (c *Client) Backup(ctx context.Context, w io.Writer) (int, error) {
	u := url.URL{Scheme: "http", Host: "agent", Path: api.PathBackup}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, err
	}
	hc := *c.http
	hc.Timeout = 0
	resp, err := hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		var e api.ErrorBody
		detail := string(bytes.TrimSpace(body))
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			detail = e.Error
		}
		return 0, help.New(help.GL4006, "status", resp.Status, "detail", detail)
	}
	v, _ := strconv.Atoi(resp.Header.Get(api.HeaderSchemaVersion))
	n, err := io.Copy(w, resp.Body)
	if err != nil {
		return 0, err
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return 0, fmt.Errorf("backup cut short: %d of %d bytes", n, resp.ContentLength)
	}
	return v, nil
}

// send makes a request with an optional JSON body.
func (c *Client) send(ctx context.Context, method, path string, in, out any) error {
	var rd io.Reader
	if in != nil {
		body, err := json.Marshal(in)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(body)
	}
	u := url.URL{Scheme: "http", Host: "agent", Path: path}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req, out)
}

// Tokens lists the access tokens of the HTTP API.
func (c *Client) Tokens(ctx context.Context) (api.Tokens, error) {
	var t api.Tokens
	err := c.get(ctx, api.PathTokens, nil, &t)
	return t, err
}

// CreateToken adds an access token and returns it with its secret.
func (c *Client) CreateToken(ctx context.Context, req api.CreateToken) (api.CreatedToken, error) {
	var out api.CreatedToken
	err := c.send(ctx, http.MethodPost, api.PathTokens, req, &out)
	return out, err
}

// RevokeToken removes the token with the given id or name.
func (c *Client) RevokeToken(ctx context.Context, idOrName string) (api.Token, error) {
	var out api.Token
	err := c.send(ctx, http.MethodDelete, api.PathTokens+"/"+url.PathEscape(idOrName), nil, &out)
	return out, err
}

// SessionCode asks for a one-time sign-in link to the interface. It returns
// GL4015 when the agent has its HTTP API off.
func (c *Client) SessionCode(ctx context.Context, role string) (api.SessionCode, error) {
	var out api.SessionCode
	err := c.send(ctx, http.MethodPost, api.PathSessionCodes, api.SessionCodeRequest{Role: role}, &out)
	if se := (*StatusError)(nil); errors.As(err, &se) && se.Status == http.StatusConflict {
		return out, help.New(help.GL4015).Wrap(err)
	}
	return out, err
}

// IsUnreachable reports whether err means the agent could not be reached.
func IsUnreachable(err error) bool {
	var e *help.Error
	return errors.As(err, &e) && e.Code == help.GL4001
}
