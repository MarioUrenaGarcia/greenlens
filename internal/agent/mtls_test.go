// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/pki"
)

// fleet is an authority with the files it issued.
type fleet struct {
	t     *testing.T
	caDir string
	out   string
	roots *x509.CertPool
}

func newFleet(t *testing.T) *fleet {
	t.Helper()
	f := &fleet{t: t, caDir: filepath.Join(t.TempDir(), "ca"), out: t.TempDir(), roots: x509.NewCertPool()}
	ca, err := pki.InitCA(f.caDir, "fleet", 30, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	f.roots.AddCert(ca)
	return f
}

func (f *fleet) issue(name, role string, at time.Time) pki.Issued {
	f.t.Helper()
	is, err := pki.Issue(f.caDir, f.out, pki.Request{Name: name, Role: role, Hosts: []string{"127.0.0.1"}, Days: 7}, at)
	if err != nil {
		f.t.Fatal(err)
	}
	return is
}

// client returns an HTTP client that trusts the authority and shows the
// certificate of is, or none when is is nil.
func (f *fleet) client(is *pki.Issued) *http.Client {
	f.t.Helper()
	conf := &tls.Config{RootCAs: f.roots, MinVersion: tls.VersionTLS12}
	if is != nil {
		pair, err := tls.LoadX509KeyPair(is.CertPath, is.KeyPath)
		if err != nil {
			f.t.Fatal(err)
		}
		conf.Certificates = []tls.Certificate{pair}
	}
	c := &http.Client{Transport: &http.Transport{TLSClientConfig: conf}}
	f.t.Cleanup(c.CloseIdleConnections)
	return c
}

func TestHubCertificateReadsTheAPI(t *testing.T) {
	f := newFleet(t)
	node := f.issue("node-a", pki.RoleAgent, time.Now())
	listen := freePort(t)
	// No token exists: the authority of the fleet is the way in.
	startAgentWith(t, "", func(c *config.Config) {
		c.Server.Enabled = true
		c.Server.Listen = listen
		c.Server.TLS = config.TLS{CertFile: node.CertPath, KeyFile: node.KeyPath, ClientCAFile: filepath.Join(f.caDir, pki.CACertFile)}
	})
	base := "https://" + listen
	hub := f.issue("hub", pki.RoleHub, time.Now())
	resp, body := call{t: t, c: f.client(&hub), base: base}.do("GET", api.PathStatus, nil)
	var st api.Status
	if resp.code != http.StatusOK || json.Unmarshal(body, &st) != nil || st.Version != "test" {
		t.Fatalf("the hub reads the status: %d %s", resp.code, body)
	}
	// A viewer reads; it does not manage tokens.
	if resp, _ := (call{t: t, c: f.client(&hub), base: base}).do("GET", api.PathTokens, nil); resp.code != http.StatusForbidden {
		t.Errorf("the hub lists tokens: %d", resp.code)
	}
	// Without a certificate the API still asks for a token.
	if resp, _ := (call{t: t, c: f.client(nil), base: base}).do("GET", api.PathStatus, nil); resp.code != http.StatusUnauthorized {
		t.Errorf("no certificate: %d", resp.code)
	}
	if resp, _ := (call{t: t, c: f.client(nil), base: base}).do("GET", api.PathHealth, nil); resp.code != http.StatusOK {
		t.Errorf("health without a certificate: %d", resp.code)
	}

	// The certificate of another agent proves it is in the fleet, which
	// does not let it read this one. It is not a client certificate, so
	// the handshake itself refuses it.
	other := f.issue("node-b", pki.RoleAgent, time.Now())
	if err := get(t, f.client(&other), base+api.PathStatus); err == nil {
		t.Error("the certificate of another agent was accepted")
	}
	// A hub of another authority is refused, and so is an expired one.
	stranger := newFleet(t).issue("hub", pki.RoleHub, time.Now())
	if err := get(t, f.client(&stranger), base+api.PathStatus); err == nil {
		t.Error("a hub of another authority was accepted")
	}
	expired := f.issue("old-hub", pki.RoleHub, time.Now().Add(-30*24*time.Hour))
	if err := get(t, f.client(&expired), base+api.PathStatus); err == nil {
		t.Error("an expired hub certificate was accepted")
	}
}

// get reports whether a request could be made at all.
func get(t *testing.T, c *http.Client, url string) error {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func TestNetworkListenerNeedsATokenOrAnAuthority(t *testing.T) {
	f := newFleet(t)
	node := f.issue("node-a", pki.RoleAgent, time.Now())
	cfg := config.Defaults()
	cfg.Server.TLS = config.TLS{ClientCAFile: filepath.Join(f.caDir, pki.CACertFile)}
	if err := cfg.Validate(); err == nil {
		t.Fatal("client certificates without TLS were accepted")
	}
	cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile = node.CertPath, node.KeyPath
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
