// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package otlp

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/intranet"
)

// Protocols of the export.
const (
	ProtocolGRPC = "grpc"
	ProtocolHTTP = "http"
)

// Protocols lists every protocol.
var Protocols = []string{ProtocolGRPC, ProtocolHTTP}

const (
	// httpPath is the default path of metrics in OTLP over HTTP.
	httpPath = "/v1/metrics"
	// grpcMethod is the Export method of the metrics service.
	grpcMethod = "/opentelemetry.proto.collector.metrics.v1.MetricsService/Export"

	contentProtobuf = "application/x-protobuf"
	contentGRPC     = "application/grpc"
	headerStatus    = "Grpc-Status"
	headerMessage   = "Grpc-Message"
	// maxAnswer bounds what is read of an answer; a collector answers
	// with a few bytes.
	maxAnswer = 64 << 10
	timeout   = 10 * time.Second
)

// CheckEndpoint checks the address of a collector: http or https, with a
// host on the internal network.
func CheckEndpoint(raw string) error {
	_, err := intranet.CheckURL(raw, "http", "https")
	return err
}

// Client sends encoded requests to one collector.
type Client struct {
	url      string
	protocol string
	token    []byte
	http     *http.Client
}

// NewClient returns a client for the collector at endpoint. With the http
// protocol an endpoint without a path gets /v1/metrics, the default of
// OTLP over HTTP. With grpc an http endpoint speaks HTTP/2 without TLS, as
// a collector on loopback expects. token, when not empty, is sent as a
// bearer credential. roots are the authorities that sign a TLS collector.
func NewClient(endpoint, protocol string, token []byte, roots *x509.CertPool) (*Client, error) {
	u, err := intranet.CheckURL(endpoint, "http", "https")
	if err != nil {
		return nil, err
	}
	tr := intranet.Transport(roots)
	switch protocol {
	case ProtocolHTTP:
		if u.Path == "" || u.Path == "/" {
			u.Path = httpPath
		}
	case ProtocolGRPC:
		if u.Path != "" && u.Path != "/" {
			return nil, errors.New("a gRPC endpoint has no path")
		}
		u.Path = grpcMethod
		// gRPC needs HTTP/2; without TLS there is no negotiation, so the
		// connection starts in HTTP/2 directly.
		tr.Protocols = new(http.Protocols)
		if u.Scheme == "https" {
			tr.Protocols.SetHTTP2(true)
		} else {
			tr.Protocols.SetUnencryptedHTTP2(true)
		}
	default:
		return nil, fmt.Errorf("unknown protocol %q", protocol)
	}
	return &Client{url: u.String(), protocol: protocol, token: token,
		http: &http.Client{Transport: tr, Timeout: timeout, CheckRedirect: intranet.NoRedirect}}, nil
}

// Close releases the idle connections.
func (c *Client) Close() { c.http.CloseIdleConnections() }

// Export sends one request and reports whether the collector took it.
func (c *Client) Export(ctx context.Context, request []byte) error {
	body, content := request, contentProtobuf
	if c.protocol == ProtocolGRPC {
		// A gRPC message is framed with a flag, zero for not compressed,
		// and its length.
		body = make([]byte, 5, 5+len(request))
		binary.BigEndian.PutUint32(body[1:], uint32(len(request))) //nolint:gosec // a request is a few kilobytes
		body, content = append(body, request...), contentGRPC
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", content)
	if c.protocol == ProtocolGRPC {
		req.Header.Set("Te", "trailers")
	}
	if len(c.token) > 0 {
		req.Header.Set("Authorization", "Bearer "+string(c.token))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	// The trailers of gRPC arrive once the body is read.
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, maxAnswer)); err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("the collector answered %s", resp.Status)
	}
	if c.protocol != ProtocolGRPC {
		return nil
	}
	return grpcStatus(resp)
}

// grpcStatus reads the status of a call, which a server sends in the
// trailers, or in the headers when it answers without a body.
func grpcStatus(resp *http.Response) error {
	status, msg := resp.Trailer.Get(headerStatus), resp.Trailer.Get(headerMessage)
	if status == "" {
		status, msg = resp.Header.Get(headerStatus), resp.Header.Get(headerMessage)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), contentGRPC) || status == "" {
		return errors.New("the answer is not gRPC; check export.otlp.protocol and the port")
	}
	code, err := strconv.Atoi(status)
	if err != nil {
		return fmt.Errorf("unreadable gRPC status %q", status)
	}
	if code != 0 {
		if m, err := url.PathUnescape(msg); err == nil {
			msg = m
		}
		return fmt.Errorf("the collector answered gRPC status %d: %s", code, msg)
	}
	return nil
}
