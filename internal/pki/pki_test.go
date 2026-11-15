// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package pki

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newCA(t *testing.T) (string, *x509.Certificate) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ca")
	ca, err := InitCA(dir, "fleet", DefaultCADays, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return dir, ca
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func TestInitCA(t *testing.T) {
	dir, ca := newCA(t)
	if !ca.IsCA || ca.Subject.CommonName != "fleet" || !ca.MaxPathLenZero || ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Fatalf("authority %+v", ca.Subject)
	}
	if d := time.Until(ca.NotAfter); d < (DefaultCADays-1)*24*time.Hour || d > (DefaultCADays+1)*24*time.Hour {
		t.Fatalf("valid for %s", d)
	}
	if mode(t, filepath.Join(dir, CAKeyFile)) != 0o600 || mode(t, filepath.Join(dir, CACertFile)) != 0o644 || mode(t, dir) != 0o700 {
		t.Fatalf("modes: key %o, certificate %o, directory %o", mode(t, filepath.Join(dir, CAKeyFile)), mode(t, filepath.Join(dir, CACertFile)), mode(t, dir))
	}
	loaded, key, err := LoadCA(dir)
	if err != nil || !loaded.Equal(ca) || !key.PublicKey.Equal(ca.PublicKey) {
		t.Fatalf("LoadCA: %v", err)
	}
	// An authority is never replaced: its certificates would stop working.
	if _, err := InitCA(dir, "fleet", DefaultCADays, time.Now()); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("a second authority in the same directory: %v", err)
	}
	if after, _, err := LoadCA(dir); err != nil || !after.Equal(ca) {
		t.Fatalf("the authority changed: %v", err)
	}
	for _, days := range []int{0, MaxCADays + 1} {
		if _, err := InitCA(t.TempDir(), "fleet", days, time.Now()); err == nil {
			t.Errorf("%d days were accepted", days)
		}
	}
	if _, err := InitCA(t.TempDir(), "", 10, time.Now()); err == nil {
		t.Error("an authority without a name was accepted")
	}
}

func TestIssue(t *testing.T) {
	caDir, ca := newCA(t)
	out := t.TempDir()
	now := time.Now()
	agent, err := Issue(caDir, out, Request{Name: "node-a", Role: RoleAgent, Hosts: []string{"10.0.0.11", "node-a.example.org", "fd00::11", "node-a"}, Days: DefaultCertDays}, now)
	if err != nil {
		t.Fatal(err)
	}
	c := agent.Cert
	if c.IsCA || c.Subject.CommonName != "node-a" || Role(c) != RoleAgent || agent.Replaced {
		t.Fatalf("certificate %+v", c.Subject)
	}
	if len(c.ExtKeyUsage) != 1 || c.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth || c.KeyUsage != x509.KeyUsageDigitalSignature {
		t.Fatalf("usages %v %v", c.ExtKeyUsage, c.KeyUsage)
	}
	if strings.Join(c.DNSNames, ",") != "node-a,node-a.example.org" || len(c.IPAddresses) != 2 ||
		!c.IPAddresses[0].Equal(net.ParseIP("10.0.0.11")) || !c.IPAddresses[1].Equal(net.ParseIP("fd00::11")) {
		t.Fatalf("names %v %v", c.DNSNames, c.IPAddresses)
	}
	if d := c.NotAfter.Sub(now); d < 29*24*time.Hour || d > 31*24*time.Hour {
		t.Fatalf("valid for %s", d)
	}
	if err := c.CheckSignatureFrom(ca); err != nil {
		t.Fatal(err)
	}
	if mode(t, agent.KeyPath) != 0o600 || agent.CertPath != filepath.Join(out, "node-a.crt") || agent.KeyPath != filepath.Join(out, "node-a.key") {
		t.Fatalf("files %s %s", agent.CertPath, agent.KeyPath)
	}
	if _, err := tls.LoadX509KeyPair(agent.CertPath, agent.KeyPath); err != nil {
		t.Fatal(err)
	}

	hub, err := Issue(caDir, out, Request{Name: "hub", Role: RoleHub, Days: 7}, now)
	if err != nil {
		t.Fatal(err)
	}
	if Role(hub.Cert) != RoleHub || len(hub.Cert.ExtKeyUsage) != 1 || hub.Cert.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Fatalf("hub certificate %+v %v", hub.Cert.Subject, hub.Cert.ExtKeyUsage)
	}
	if hub.Cert.SerialNumber.Cmp(c.SerialNumber) == 0 {
		t.Fatal("two certificates share a serial number")
	}

	// Issuing the same name again renews it with a new key.
	again, err := Issue(caDir, out, Request{Name: "node-a", Role: RoleAgent, Days: DefaultCertDays}, now)
	if err != nil || !again.Replaced || again.Cert.PublicKey.(*ecdsa.PublicKey).Equal(c.PublicKey) {
		t.Fatalf("renewal: replaced %v, %v", again.Replaced, err)
	}
	if _, err := tls.LoadX509KeyPair(again.CertPath, again.KeyPath); err != nil {
		t.Fatal(err)
	}
}

func TestIssueErrors(t *testing.T) {
	caDir, _ := newCA(t)
	out := t.TempDir()
	now := time.Now()
	for name, req := range map[string]Request{
		"an upper case name":     {Name: "Node-A", Role: RoleAgent, Days: 30},
		"a name with a path":     {Name: "../node", Role: RoleAgent, Days: 30},
		"no name":                {Role: RoleAgent, Days: 30},
		"an unknown role":        {Name: "node-a", Role: "admin", Days: 30},
		"no validity":            {Name: "node-a", Role: RoleAgent},
		"too long a validity":    {Name: "node-a", Role: RoleAgent, Days: MaxCertDays + 1},
		"a host that is not one": {Name: "node-a", Role: RoleAgent, Days: 30, Hosts: []string{"not a host"}},
		"a host with a wildcard": {Name: "node-a", Role: RoleAgent, Days: 30, Hosts: []string{"*.example.org"}},
	} {
		if _, err := Issue(caDir, out, req, now); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 0 {
		t.Fatalf("refused requests left files: %v %v", entries, err)
	}
	if _, err := Issue(t.TempDir(), out, Request{Name: "node-a", Role: RoleAgent, Days: 30}, now); err == nil {
		t.Error("a directory without an authority was accepted")
	}
	// A certificate cannot outlive its authority.
	short := filepath.Join(t.TempDir(), "ca")
	if _, err := InitCA(short, "short", 10, now); err != nil {
		t.Fatal(err)
	}
	if _, err := Issue(short, out, Request{Name: "node-a", Role: RoleAgent, Days: 30}, now); err == nil || !strings.Contains(err.Error(), "expires") {
		t.Errorf("a certificate that outlives its authority: %v", err)
	}
	if _, err := Issue(short, out, Request{Name: "node-a", Role: RoleAgent, Days: 5}, now); err != nil {
		t.Errorf("a certificate within the life of its authority: %v", err)
	}
}

func TestLoadCAErrors(t *testing.T) {
	caDir, _ := newCA(t)
	other, _ := newCA(t)
	mixed := t.TempDir()
	copyFile(t, filepath.Join(caDir, CACertFile), filepath.Join(mixed, CACertFile))
	copyFile(t, filepath.Join(other, CAKeyFile), filepath.Join(mixed, CAKeyFile))
	if _, _, err := LoadCA(mixed); err == nil || !strings.Contains(err.Error(), "is not the key of") {
		t.Fatalf("a key of another authority: %v", err)
	}
	// The certificate of a node is not an authority.
	leafDir := t.TempDir()
	leaf, err := Issue(caDir, leafDir, Request{Name: "node-a", Role: RoleAgent, Days: 30}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	fake := t.TempDir()
	copyFile(t, leaf.CertPath, filepath.Join(fake, CACertFile))
	copyFile(t, leaf.KeyPath, filepath.Join(fake, CAKeyFile))
	if _, _, err := LoadCA(fake); err == nil || !strings.Contains(err.Error(), "not the certificate of an authority") {
		t.Fatalf("a node certificate used as an authority: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fake, CACertFile), []byte("not PEM"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCert(filepath.Join(fake, CACertFile)); err == nil {
		t.Fatal("a file without a certificate was accepted")
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The certificates work for what they are issued for and for nothing
// else: a hub reads an agent with mutual TLS, an agent certificate cannot
// be used as a client, and a certificate of another authority is refused.
func TestMutualTLS(t *testing.T) {
	caDir, ca := newCA(t)
	out := t.TempDir()
	now := time.Now()
	issue := func(dir string, req Request) tls.Certificate {
		t.Helper()
		is, err := Issue(dir, out, req, now)
		if err != nil {
			t.Fatal(err)
		}
		pair, err := tls.LoadX509KeyPair(is.CertPath, is.KeyPath)
		if err != nil {
			t.Fatal(err)
		}
		return pair
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Client", Role(r.TLS.PeerCertificates[0])+":"+r.TLS.PeerCertificates[0].Subject.CommonName)
	}))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{issue(caDir, Request{Name: "node-a", Role: RoleAgent, Hosts: []string{"127.0.0.1"}, Days: 30})},
		ClientCAs:    roots, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12,
	}
	srv.StartTLS()
	defer srv.Close()
	get := func(client *tls.Certificate, trust *x509.CertPool) (string, error) {
		cfg := &tls.Config{RootCAs: trust, MinVersion: tls.VersionTLS12}
		if client != nil {
			cfg.Certificates = []tls.Certificate{*client}
		}
		c := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}
		defer c.CloseIdleConnections()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := c.Do(req)
		if err != nil {
			return "", err
		}
		return resp.Header.Get("X-Client"), resp.Body.Close()
	}
	hub := issue(caDir, Request{Name: "hub", Role: RoleHub, Days: 30})
	if got, err := get(&hub, roots); err != nil || got != "hub:hub" {
		t.Fatalf("the hub reads the agent: %q %v", got, err)
	}
	if _, err := get(nil, roots); err == nil {
		t.Fatal("a client without a certificate was served")
	}
	node := issue(caDir, Request{Name: "node-b", Role: RoleAgent, Days: 30})
	if _, err := get(&node, roots); err == nil {
		t.Fatal("the certificate of an agent was accepted as a client")
	}
	otherDir, otherCA := newCA(t)
	stranger := issue(otherDir, Request{Name: "hub", Role: RoleHub, Days: 30})
	if _, err := get(&stranger, roots); err == nil {
		t.Fatal("a certificate of another authority was accepted")
	}
	// The hub also refuses an agent that another authority signed.
	otherRoots := x509.NewCertPool()
	otherRoots.AddCert(otherCA)
	if _, err := get(&hub, otherRoots); err == nil {
		t.Fatal("an agent of another authority was trusted")
	}
	// A certificate that has expired is refused.
	old, err := Issue(caDir, out, Request{Name: "old-hub", Role: RoleHub, Days: 1}, now.Add(-48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	expired, err := tls.LoadX509KeyPair(old.CertPath, old.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := get(&expired, roots); err == nil {
		t.Fatal("an expired certificate was accepted")
	}
}

func TestValidName(t *testing.T) {
	for name, want := range map[string]bool{
		"node-a": true, "gpu01.rack2.example.org": true, "a": true, "10": true,
		"": false, "Node": false, "-node": false, "node_a": false, "node a": false, "../x": false, strings.Repeat("a", 64): false,
	} {
		if ValidName(name) != want {
			t.Errorf("ValidName(%q) = %v", name, !want)
		}
	}
}

func TestKeyPairReloadsARenewedCertificate(t *testing.T) {
	caDir, _ := newCA(t)
	out := t.TempDir()
	first, err := Issue(caDir, out, Request{Name: "node-a", Role: RoleAgent, Days: 30}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var failures []error
	pair, err := NewKeyPair(first.CertPath, first.KeyPath, func(err error) { failures = append(failures, err) })
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	pair.now = func() time.Time { return now }
	served := func() string {
		t.Helper()
		c, err := pair.Server(nil)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(c.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		return leaf.SerialNumber.String()
	}
	if served() != first.Cert.SerialNumber.String() {
		t.Fatal("the first certificate is not served")
	}
	renewed, err := Issue(caDir, out, Request{Name: "node-a", Role: RoleAgent, Days: 30}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// The files are not read again before ReloadEvery has passed.
	if served() != first.Cert.SerialNumber.String() {
		t.Fatal("the files were read again too soon")
	}
	now = now.Add(ReloadEvery)
	// File systems with coarse times could give both files the times they
	// had; the renewal must still be seen.
	later := time.Now().Add(time.Hour)
	for _, f := range []string{renewed.CertPath, renewed.KeyPath} {
		if err := os.Chtimes(f, later, later); err != nil {
			t.Fatal(err)
		}
	}
	if served() != renewed.Cert.SerialNumber.String() {
		t.Fatal("the renewed certificate is not served")
	}
	if c, err := pair.Client(nil); err != nil || c == nil {
		t.Fatalf("client certificate: %v", err)
	}
	// A key that does not match yet keeps the previous pair and reports
	// the failure.
	other, err := Issue(caDir, t.TempDir(), Request{Name: "node-a", Role: RoleAgent, Days: 30}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	copyFile(t, other.KeyPath, renewed.KeyPath)
	now = now.Add(ReloadEvery)
	if served() != renewed.Cert.SerialNumber.String() || len(failures) != 1 {
		t.Fatalf("a mismatched pair: %d failures", len(failures))
	}
	if _, err := NewKeyPair(first.CertPath, filepath.Join(out, "missing.key"), func(error) {}); err == nil {
		t.Fatal("a missing key was accepted")
	}
}
