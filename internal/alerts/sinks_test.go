// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func note() Notification {
	at := time.Date(2026, 10, 21, 10, 10, 0, 0, time.UTC)
	return Notification{SchemaVersion: 1, Agent: "test", Alert: Alert{ID: 1, Rule: "high_jpt", Expr: ModelJPerToken, Op: ">", Threshold: 5, ForS: 600,
		Labels: map[string]string{"backend": "ollama", "model": "qwen"}, State: Firing, Value: 6.5, StartedAt: at.Add(-10 * time.Minute), FiredAt: &at}}
}

func TestWebhookIsSigned(t *testing.T) {
	token := []byte("s3cret-token")
	var got Notification
	var sigOK bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ts := r.Header.Get(HeaderTimestamp)
		sigOK = r.Header.Get("Authorization") == "Bearer s3cret-token" && r.Header.Get(HeaderSignature) == Sign(token, ts, body)
		if !strings.Contains(string(body), `"op":">"`) {
			t.Errorf("escaped comparison in %s", body)
		}
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	s, err := NewWebhook(srv.URL+"/hook", token)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), note()); err != nil {
		t.Fatal(err)
	}
	if !sigOK || got.Rule != "high_jpt" || got.State != Firing || got.Labels["model"] != "qwen" || got.Value != 6.5 {
		t.Fatalf("signature %v, body %+v", sigOK, got)
	}
	// A receiver that refuses is an error, so the dispatcher retries.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer bad.Close()
	s, _ = NewWebhook(bad.URL, token)
	if err := s.Send(context.Background(), note()); err == nil {
		t.Fatal("401 accepted")
	}
}

func TestWebhookStaysInside(t *testing.T) {
	for _, u := range []string{"http://192.0.2.10/hook", "https://[2001:db8::1]/hook", "ftp://10.0.0.1/x", "http:///x"} {
		if _, err := NewWebhook(u, []byte("t")); err == nil {
			t.Errorf("%s accepted", u)
		}
	}
	for _, u := range []string{"http://127.0.0.1:9000/hook", "https://10.1.2.3/hook", "http://[fd00::1]/hook", "http://hooks.example.org/x"} {
		if err := CheckURL(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	if _, err := NewWebhook("http://127.0.0.1/x", nil); err == nil {
		t.Error("no token accepted")
	}
	// A name is checked by the address it resolves to, when connecting.
	s, err := NewWebhook("http://hooks.example.org/x", []byte("t"))
	if err != nil {
		t.Fatal(err)
	}
	d := s.Client.Transport.(*http.Transport).DialContext
	if _, err := d(context.Background(), "tcp", "192.0.2.10:80"); !errors.Is(err, ErrNotInternal) {
		t.Fatalf("outside address: %v", err)
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.1.1", "fd00::1", "fe80::1", "::ffff:10.0.0.1"} {
		if !Internal(netip.MustParseAddr(ip)) {
			t.Errorf("%s not internal", ip)
		}
	}
	for _, ip := range []string{"192.0.2.10", "198.51.100.1", "2001:db8::1", "8.8.8.8"} {
		if Internal(netip.MustParseAddr(ip)) {
			t.Errorf("%s internal", ip)
		}
	}
}

// listenUnixgram returns a datagram socket in a short temporary directory
// and a channel with what it receives.
func listenUnixgram(t *testing.T) (string, <-chan string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "gla")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s")
	c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	got := make(chan string, 4)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := c.Read(buf)
			if err != nil {
				return
			}
			got <- string(buf[:n])
		}
	}()
	return path, got
}

func TestJournalAndSyslog(t *testing.T) {
	path, got := listenUnixgram(t)
	if err := (JournalSink{Socket: path}).Send(context.Background(), note()); err != nil {
		t.Fatal(err)
	}
	msg := <-got
	for _, want := range []string{"MESSAGE=alert high_jpt firing: model_joules_per_token backend=ollama,model=qwen > 5 (value 6.5)\n",
		"PRIORITY=4\n", "SYSLOG_IDENTIFIER=greenlens\n", "GREENLENS_ALERT_RULE=high_jpt\n"} {
		if !strings.Contains(msg, want) {
			t.Errorf("journal lacks %q in %q", want, msg)
		}
	}
	n := note()
	n.State = Resolved
	if err := (SyslogSink{Socket: path}).Send(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if msg := <-got; !strings.HasPrefix(msg, "<30>") || !strings.Contains(msg, " greenlens: alert high_jpt resolved") {
		t.Errorf("syslog %q", msg)
	}
	// A series without labels reads as its signal alone.
	n.Expr, n.Labels = NodePowerW, map[string]string{}
	if s := summary(n); s != "alert high_jpt resolved: node_power_watts > 5 (value 6.5)" {
		t.Errorf("summary %q", s)
	}
}

type flaky struct {
	fails int
	sent  chan Notification
}

func (f *flaky) Name() string { return "flaky" }
func (f *flaky) Send(_ context.Context, n Notification) error {
	if f.fails > 0 {
		f.fails--
		return errors.New("down")
	}
	f.sent <- n
	return nil
}

func TestDispatcherRetries(t *testing.T) {
	old := Retries
	Retries = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { Retries = old })
	f := &flaky{fails: 2, sent: make(chan Notification, 1)}
	d := NewDispatcher(slog.New(slog.NewTextHandler(io.Discard, nil)), []Sink{f})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	d.Notify(note())
	select {
	case n := <-f.sent:
		if n.Rule != "high_jpt" {
			t.Fatalf("%+v", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("not delivered after two failures")
	}
	cancel()
	<-done
}
