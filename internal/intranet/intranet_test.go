// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package intranet

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

func TestInternal(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "10.1.2.3": true, "172.16.0.9": true, "192.168.1.20": true,
		"169.254.10.1": true, "fe80::1": true, "fd00::5": true, "::ffff:10.0.0.1": true,
		"192.0.2.10": false, "203.0.113.7": false, "2001:db8::1": false, "::ffff:203.0.113.7": false,
	} {
		if got := Internal(netip.MustParseAddr(addr)); got != want {
			t.Errorf("Internal(%s) = %v", addr, got)
		}
	}
}

func TestCheckURL(t *testing.T) {
	for _, ok := range []string{"http://127.0.0.1:4318", "https://collector.example.org/v1/metrics", "http://[fd00::5]:4318"} {
		if _, err := CheckURL(ok, "http", "https"); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "collector:4317", "ftp://10.0.0.1", "http://", "http://user:secret@10.0.0.1", "http://%zz"} {
		if _, err := CheckURL(bad, "http", "https"); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	if _, err := CheckURL("http://192.0.2.10", "http"); !errors.Is(err, ErrNotInternal) {
		t.Errorf("an outside address: %v", err)
	}
	if _, err := CheckURL("http://10.0.0.1", "https"); err == nil {
		t.Error("a scheme outside the list was accepted")
	}
}

func TestDialerRefusesOutsideAddresses(t *testing.T) {
	if _, err := Dialer().DialContext(context.Background(), "tcp", "192.0.2.10:80"); !errors.Is(err, ErrNotInternal) {
		t.Fatalf("an outside address: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	c := &http.Client{Transport: Transport(nil), CheckRedirect: NoRedirect}
	if code, err := status(t, c, srv.URL); err != nil || code != http.StatusNoContent {
		t.Fatalf("status %d, %v", code, err)
	}
}

func TestRoots(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(ca, pemOf(srv.Certificate().Raw), 0o600); err != nil {
		t.Fatal(err)
	}
	roots, err := Roots(ca)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := status(t, &http.Client{Transport: Transport(roots)}, srv.URL); err != nil {
		t.Fatal(err)
	}
	// Without the authority of the server its certificate is not trusted.
	sys, err := Roots("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := status(t, &http.Client{Transport: Transport(sys)}, srv.URL); err == nil {
		t.Fatal("an unknown authority was trusted")
	}
	empty := filepath.Join(dir, "empty.pem")
	if err := os.WriteFile(empty, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Roots(empty); err == nil {
		t.Fatal("a file without certificates was accepted")
	}
	if _, err := Roots(filepath.Join(dir, "missing.pem")); err == nil {
		t.Fatal("a missing file was accepted")
	}
}

func pemOf(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// status returns the status code of a GET.
func status(t *testing.T, c *http.Client, url string) (int, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}
