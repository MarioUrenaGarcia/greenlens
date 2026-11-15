// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package intranet builds HTTP clients that only reach loopback, private
// and link-local addresses. GreenLens makes no outbound connection by
// default; the opt-in integrations that make one, such as an OpenTelemetry
// collector or the management controller of a server, use these clients so
// a wrong or hostile name cannot send data outside the internal network.
package intranet

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"syscall"
	"time"
)

// ErrNotInternal is returned for an address that is not loopback, private
// or link-local.
var ErrNotInternal = errors.New("only loopback, private or link-local addresses may be reached")

// Internal reports whether an address is loopback, private or link-local.
func Internal(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// CheckURL checks the form of a URL: one of the schemes, with a host and
// without credentials. The addresses a host name resolves to are checked
// at each connection.
func CheckURL(raw string, schemes ...string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(schemes, u.Scheme) || u.Hostname() == "" {
		return nil, fmt.Errorf("the URL must start with %s:// and name a host", schemes[0])
	}
	if u.User != nil {
		return nil, errors.New("the URL must not carry credentials")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !Internal(ip) {
		return nil, ErrNotInternal
	}
	return u, nil
}

// Control refuses connections to addresses that are not internal. It runs
// after the name is resolved, so a name that points outside is refused
// too.
func Control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || !Internal(ap.Addr()) {
		return ErrNotInternal
	}
	return nil
}

// Dialer returns a dialer that only connects to internal addresses.
func Dialer() *net.Dialer {
	return &net.Dialer{Timeout: 5 * time.Second, Control: Control}
}

// Roots returns the certificate authorities that sign the servers: those
// of a PEM file, or the ones of the system when the path is empty. It
// reads them now, so a caller that later confines its file access keeps
// them.
func Roots(caFile string) (*x509.CertPool, error) {
	if caFile == "" {
		return x509.SystemCertPool()
	}
	pem, err := os.ReadFile(caFile) //nolint:gosec // a path of the configuration
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%s holds no PEM certificate", caFile)
	}
	return pool, nil
}

// Transport returns an HTTP transport that only connects to internal
// addresses, ignores proxy settings of the environment and verifies TLS
// servers against the given roots.
func Transport(roots *x509.CertPool) *http.Transport {
	return &http.Transport{
		DialContext:           Dialer().DialContext,
		Proxy:                 nil,
		TLSClientConfig:       &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          2,
		IdleConnTimeout:       time.Minute,
	}
}

// NoRedirect keeps a client from following redirections, which could lead
// outside the address that was checked.
func NoRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
