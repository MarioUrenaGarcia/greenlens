// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package alerts

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Notification is what a sink receives when an alert fires or is
// resolved.
type Notification struct {
	SchemaVersion int    `json:"schema_version"`
	Agent         string `json:"agent_version"`
	Alert
}

// Sink delivers notifications.
type Sink interface {
	Name() string
	Send(ctx context.Context, n Notification) error
}

// summary is the one-line text of a notification for logs.
func summary(n Notification) string {
	var labels []string
	for _, k := range slices.Sorted(maps.Keys(n.Labels)) {
		labels = append(labels, k+"="+n.Labels[k])
	}
	series := n.Expr
	if len(labels) > 0 {
		series += " " + strings.Join(labels, ",")
	}
	return fmt.Sprintf("alert %s %s: %s %s %s (value %s)", n.Rule, n.State, series,
		n.Op, strconv.FormatFloat(n.Threshold, 'g', -1, 64), strconv.FormatFloat(n.Value, 'g', 6, 64))
}

// LogSink writes notifications to the log of the agent.
type LogSink struct{ Log *slog.Logger }

// Name implements Sink.
func (LogSink) Name() string { return "log" }

// Send implements Sink.
func (s LogSink) Send(ctx context.Context, n Notification) error {
	level := slog.LevelWarn
	if n.State == Resolved {
		level = slog.LevelInfo
	}
	args := []any{"rule", n.Rule, "state", n.State, "expr", n.Expr, "op", n.Op, "threshold", n.Threshold, "value", n.Value}
	for _, k := range slices.Sorted(maps.Keys(n.Labels)) {
		args = append(args, k, n.Labels[k])
	}
	s.Log.Log(ctx, level, "alert "+n.State, args...)
	return nil
}

// Default paths of the local log daemons.
const (
	JournalSocket = "/run/systemd/journal/socket"
	SyslogSocket  = "/dev/log"
)

// JournalSink sends notifications to journald with its native protocol,
// one datagram per notification, with the fields of the alert.
type JournalSink struct{ Socket string }

// Name implements Sink.
func (JournalSink) Name() string { return "journald" }

// Send implements Sink.
func (s JournalSink) Send(ctx context.Context, n Notification) error {
	priority := "4" // warning
	if n.State == Resolved {
		priority = "6" // info
	}
	var b bytes.Buffer
	field := func(k, v string) {
		// Values with a newline need the binary form of the protocol;
		// none of these has one, so they are replaced.
		b.WriteString(k + "=" + strings.ReplaceAll(v, "\n", " ") + "\n")
	}
	field("MESSAGE", summary(n))
	field("PRIORITY", priority)
	field("SYSLOG_IDENTIFIER", "greenlens")
	field("GREENLENS_ALERT_RULE", n.Rule)
	field("GREENLENS_ALERT_STATE", n.State)
	field("GREENLENS_ALERT_VALUE", strconv.FormatFloat(n.Value, 'g', -1, 64))
	return sendUnixgram(ctx, s.Socket, b.Bytes())
}

// SyslogSink sends notifications to the local syslog daemon, in the format
// of RFC 3164 that local daemons accept, with the daemon facility.
type SyslogSink struct{ Socket string }

// Name implements Sink.
func (SyslogSink) Name() string { return "syslog" }

// Send implements Sink.
func (s SyslogSink) Send(ctx context.Context, n Notification) error {
	// Facility daemon (3) times 8, plus warning (4) or info (6).
	pri := 3*8 + 4
	if n.State == Resolved {
		pri = 3*8 + 6
	}
	msg := fmt.Sprintf("<%d>%s greenlens: %s", pri, time.Now().Format(time.Stamp), summary(n))
	return sendUnixgram(ctx, s.Socket, []byte(msg))
}

func sendUnixgram(ctx context.Context, path string, msg []byte) error {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unixgram", path)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetWriteDeadline(dl)
	}
	_, err = c.Write(msg)
	return err
}

// Headers of the webhook.
const (
	HeaderTimestamp = "X-GreenLens-Timestamp"
	HeaderSignature = "X-GreenLens-Signature"
)

// Sign returns the signature of a webhook body: the hex HMAC-SHA256, keyed
// with the token, of the timestamp, a dot and the body.
func Sign(token []byte, timestamp string, body []byte) string {
	m := hmac.New(sha256.New, token)
	m.Write([]byte(timestamp + "."))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// ErrNotInternal is returned when a webhook would reach an address that is
// not loopback, private or link-local.
var ErrNotInternal = errors.New("the webhook may only reach loopback, private or link-local addresses")

// Internal reports whether an address may receive webhooks.
func Internal(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// CheckURL checks the form of a webhook URL: http or https, with a host.
// The addresses the host resolves to are checked at each connection.
func CheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return fmt.Errorf("the webhook URL must be http or https with a host")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !Internal(ip) {
		return ErrNotInternal
	}
	return nil
}

// WebhookSink posts each notification as JSON to an internal URL, with
// the token as a bearer credential and a signature of the body.
type WebhookSink struct {
	URL    string
	Token  []byte
	Client *http.Client
}

// NewWebhook returns a webhook sink whose connections refuse any address
// that is not internal, after the name is resolved, so a name that points
// outside cannot be used to send data out.
func NewWebhook(rawURL string, token []byte) (*WebhookSink, error) {
	if err := CheckURL(rawURL); err != nil {
		return nil, err
	}
	if len(token) == 0 {
		return nil, errors.New("the webhook needs a token")
	}
	d := &net.Dialer{Timeout: 5 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil || !Internal(ap.Addr()) {
			return ErrNotInternal
		}
		return nil
	}}
	tr := &http.Transport{DialContext: d.DialContext, Proxy: nil, TLSHandshakeTimeout: 5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second, MaxIdleConns: 2, IdleConnTimeout: time.Minute}
	return &WebhookSink{URL: rawURL, Token: token, Client: &http.Client{Transport: tr, Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Name implements Sink.
func (*WebhookSink) Name() string { return "webhook" }

// Send implements Sink.
func (s *WebhookSink) Send(ctx context.Context, n Notification) error {
	// Comparisons such as ">" are sent as they are, not as \u003e.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(n); err != nil {
		return err
	}
	body := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+string(s.Token))
	req.Header.Set(HeaderTimestamp, ts)
	req.Header.Set(HeaderSignature, Sign(s.Token, ts, body))
	resp, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("the webhook answered %s", resp.Status)
	}
	return nil
}
