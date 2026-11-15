// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"crypto/tls"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/pki"
)

func TestHubCertificates(t *testing.T) {
	dir := t.TempDir()
	caDir, out := filepath.Join(dir, "ca"), filepath.Join(dir, "certs")

	code, stdout, errOut := runWith(t, hooks{}, nil, "hub", "init-ca", "--dir", caDir, "--name", "test-fleet")
	if code != 0 || !strings.Contains(stdout, "Authority test-fleet created") || !strings.Contains(stdout, pki.CAKeyFile) {
		t.Fatalf("init-ca: %d %s %s", code, stdout, errOut)
	}
	ca, _, err := pki.LoadCA(caDir)
	if err != nil || ca.Subject.CommonName != "test-fleet" {
		t.Fatalf("authority: %v", err)
	}
	// A second authority in the same directory is refused and changes
	// nothing.
	code, _, errOut = runWith(t, hooks{}, nil, "hub", "init-ca", "--dir", caDir)
	if code != 1 || !strings.Contains(errOut, "GL4016") || !strings.Contains(errOut, "already exists") {
		t.Fatalf("second init-ca: %d %s", code, errOut)
	}
	if again, _, err := pki.LoadCA(caDir); err != nil || !again.Equal(ca) {
		t.Fatalf("the authority changed: %v", err)
	}

	code, stdout, errOut = runWith(t, hooks{}, nil, "hub", "issue-cert", "--ca-dir", caDir, "--out", out,
		"--name", "node-a", "--host", "10.0.0.11", "--host", "node-a.example.org", "-o", "json")
	var doc certDoc
	if code != 0 || json.Unmarshal([]byte(stdout), &doc) != nil || doc.Name != "node-a" || doc.Role != pki.RoleAgent || doc.Replaced {
		t.Fatalf("issue-cert: %d %s %s", code, stdout, errOut)
	}
	if d := time.Until(doc.NotAfter); d < 29*24*time.Hour || d > 31*24*time.Hour {
		t.Fatalf("valid for %s", d)
	}
	pair, err := tls.LoadX509KeyPair(doc.CertFile, doc.KeyFile)
	if err != nil || doc.CertFile != filepath.Join(out, "node-a.crt") {
		t.Fatalf("pair %s: %v", doc.CertFile, err)
	}
	leaf, err := pki.LoadCert(doc.CertFile)
	if err != nil || leaf.CheckSignatureFrom(ca) != nil || len(leaf.IPAddresses) != 1 || len(leaf.DNSNames) != 2 || len(pair.Certificate) != 1 {
		t.Fatalf("certificate: %v %v %v", err, leaf.IPAddresses, leaf.DNSNames)
	}
	if st, err := os.Stat(doc.KeyFile); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key mode: %v", err)
	}

	// Issuing the name again renews it, and says so.
	code, stdout, _ = runWith(t, hooks{}, map[string]string{"GREENLENS_LANG": "es"}, "hub", "issue-cert", "--ca-dir", caDir, "--out", out, "--name", "node-a")
	if code != 0 || !strings.Contains(stdout, "reemplaza el certificado anterior") || !strings.Contains(stdout, "server.tls.client_ca_file") {
		t.Fatalf("renewal: %d %s", code, stdout)
	}
	code, stdout, _ = runWith(t, hooks{}, nil, "hub", "issue-cert", "--ca-dir", caDir, "--out", out, "--name", "hub", "--role", "hub", "--days", "7")
	if code != 0 || !strings.Contains(stdout, "(hub)") || !strings.Contains(stdout, "shows this certificate") {
		t.Fatalf("hub certificate: %d %s", code, stdout)
	}

	for _, args := range [][]string{
		{"init-ca"},
		{"init-ca", "--dir", filepath.Join(dir, "x"), "--days", "0"},
		{"issue-cert", "--out", out, "--name", "node-b"},
		{"issue-cert", "--ca-dir", caDir, "--name", "node-b"},
		{"issue-cert", "--ca-dir", caDir, "--out", out, "--name", "Node B"},
		{"issue-cert", "--ca-dir", caDir, "--out", out, "--name", "node-b", "--role", "admin"},
		{"issue-cert", "--ca-dir", caDir, "--out", out, "--name", "node-b", "--days", "5000"},
	} {
		if code, _, errOut := runWith(t, hooks{}, nil, append([]string{"hub"}, args...)...); code != 2 || !strings.Contains(errOut, "GL0005") {
			t.Errorf("%v: %d %s", args, code, errOut)
		}
	}
	if code, _, errOut := runWith(t, hooks{}, nil, "hub", "issue-cert", "--ca-dir", filepath.Join(dir, "missing"), "--out", out, "--name", "node-b"); code != 1 || !strings.Contains(errOut, "GL4016") {
		t.Errorf("a missing authority: %d %s", code, errOut)
	}
	if code, _, errOut := runWith(t, hooks{}, nil, "hub", "issue-cert", "--ca-dir", caDir, "--out", out, "--name", "node-b", "--host", "not a host"); code != 1 || !strings.Contains(errOut, "GL4016") {
		t.Errorf("a bad host: %d %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(out, "node-b.crt")); err == nil {
		t.Error("a refused request left a certificate")
	}
}
