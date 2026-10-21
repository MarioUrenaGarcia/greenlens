// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/auth"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/sourcetest"
)

// socketClient talks to the agent over its Unix socket, as the command line
// does.
func socketClient(path string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}}}
}

type call struct {
	t      *testing.T
	c      *http.Client
	base   string
	header http.Header
}

// reply is a response whose body has been read and closed.
type reply struct {
	code    int
	header  http.Header
	cookies []*http.Cookie
}

func (c call) do(method, path string, body any, extra ...string) (reply, []byte) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, c.base+path, rd)
	if err != nil {
		c.t.Fatal(err)
	}
	for k, v := range c.header {
		req.Header[k] = v
	}
	for i := 0; i+1 < len(extra); i += 2 {
		req.Header.Set(extra[i], extra[i+1])
	}
	resp, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return reply{code: resp.StatusCode, header: resp.Header, cookies: resp.Cookies()}, data
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func TestHTTPAPI(t *testing.T) {
	listen := freePort(t)
	r := startAgentWith(t, "", func(c *config.Config) {
		c.Server.Enabled = true
		c.Server.Listen = listen
	})
	base := "http://" + listen
	anon := call{t: t, c: &http.Client{CheckRedirect: noRedirect}, base: base}
	sock := call{t: t, c: socketClient(r.socket), base: "http://agent"}

	resp, _ := anon.do("GET", api.PathHealth, nil)
	if resp.code != 200 || !strings.Contains(resp.header.Get("Content-Security-Policy"), "frame-ancestors 'none'") ||
		resp.header.Get("X-Content-Type-Options") != "nosniff" || resp.header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("health: %d %v", resp.code, resp.header)
	}
	for _, p := range []string{api.PathStatus, api.PathUsage, api.PathBackup, api.PathTokens, api.PathAudit, api.PathSession} {
		if resp, _ := anon.do("GET", p, nil); resp.code != http.StatusUnauthorized {
			t.Errorf("%s without credentials: %d", p, resp.code)
		}
	}
	if resp, _ := anon.do("GET", api.PathStatus, nil, "Authorization", "Bearer greenlens_000000000000_"+strings.Repeat("a", 43)); resp.code != http.StatusUnauthorized {
		t.Errorf("unknown token: %d", resp.code)
	}

	// The interface is public and served compressed with an ETag.
	resp, body := anon.do("GET", "/", nil, "Accept-Encoding", "gzip")
	if resp.code != 200 || resp.header.Get("Content-Encoding") != "gzip" || resp.header.Get("ETag") == "" {
		t.Fatalf("index: %d %v", resp.code, resp.header)
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if page, _ := io.ReadAll(zr); !strings.Contains(string(page), "<html") {
		t.Fatalf("index body %q", page)
	}
	if resp, _ := anon.do("GET", "/", nil, "If-None-Match", resp.header.Get("ETag")); resp.code != http.StatusNotModified {
		t.Errorf("revalidation: %d", resp.code)
	}
	if resp, _ := anon.do("GET", "/missing.js", nil); resp.code != http.StatusNotFound {
		t.Errorf("missing file: %d", resp.code)
	}

	// A local user gets a one-time link that opens a session.
	resp, body = sock.do("POST", api.PathSessionCodes, api.SessionCodeRequest{Role: "operator"})
	var code api.SessionCode
	if resp.code != 200 || json.Unmarshal(body, &code) != nil || !strings.HasPrefix(code.URL, base+api.PathLogin+"?code=") {
		t.Fatalf("code: %d %s", resp.code, body)
	}
	link := strings.TrimPrefix(code.URL, base)
	resp, _ = anon.do("GET", link, nil)
	cookies := resp.cookies
	if resp.code != http.StatusSeeOther || resp.header.Get("Location") != "/" || len(cookies) != 1 ||
		!cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Name != api.SessionCookie {
		t.Fatalf("login: %d %v %v", resp.code, resp.header, cookies)
	}
	if resp, _ := anon.do("GET", link, nil); !strings.Contains(resp.header.Get("Location"), "expired") || len(resp.cookies) != 0 {
		t.Fatal("a code worked twice")
	}
	browser := anon
	browser.header = http.Header{"Cookie": {cookies[0].Name + "=" + cookies[0].Value}}
	resp, body = browser.do("GET", api.PathSession, nil)
	var sess api.Session
	if resp.code != 200 || json.Unmarshal(body, &sess) != nil || sess.Role != "operator" || sess.Via != auth.ViaSession || len(sess.CSRF) != 43 {
		t.Fatalf("session: %d %s", resp.code, body)
	}
	if resp, _ := browser.do("GET", api.PathStatus, nil); resp.code != 200 {
		t.Fatalf("status with a session: %d", resp.code)
	}
	if resp, _ := browser.do("GET", api.PathTokens, nil); resp.code != http.StatusForbidden {
		t.Errorf("an operator listed tokens: %d", resp.code)
	}
	// Requests that change something need the CSRF token and the same origin.
	if resp, _ := browser.do("POST", api.PathCalibrations, map[string]any{}); resp.code != http.StatusForbidden {
		t.Errorf("no CSRF token: %d", resp.code)
	}
	if resp, _ := browser.do("POST", api.PathCalibrations, map[string]any{}, api.HeaderCSRF, sess.CSRF, "Origin", "https://evil.example.com"); resp.code != http.StatusForbidden {
		t.Errorf("another origin: %d", resp.code)
	}
	if resp, _ := browser.do("POST", api.PathCalibrations, map[string]any{}, api.HeaderCSRF, sess.CSRF, "Sec-Fetch-Site", "cross-site"); resp.code != http.StatusForbidden {
		t.Errorf("cross-site request: %d", resp.code)
	}
	if resp, _ := browser.do("POST", api.PathCalibrations, map[string]any{}, api.HeaderCSRF, sess.CSRF, "Origin", base, "Sec-Fetch-Site", "same-origin"); resp.code != http.StatusBadRequest {
		t.Errorf("a valid request did not reach the handler: %d", resp.code)
	}
	if resp, _ := browser.do("POST", api.PathSessionCodes, api.SessionCodeRequest{}, api.HeaderCSRF, sess.CSRF); resp.code == 200 {
		t.Error("sign-in codes are handed out over HTTP")
	}

	// Tokens are created on the socket and shown once.
	resp, body = sock.do("POST", api.PathTokens, api.CreateToken{Name: "grafana", Role: "viewer", ExpiresInS: 3600})
	var created api.CreatedToken
	if resp.code != http.StatusCreated || json.Unmarshal(body, &created) != nil || !strings.HasPrefix(created.Secret, auth.TokenPrefix) || created.Token.ExpiresAt == nil {
		t.Fatalf("create token: %d %s", resp.code, body)
	}
	viewer := anon
	viewer.header = http.Header{"Authorization": {"Bearer " + created.Secret}}
	if resp, _ := viewer.do("GET", api.PathStatus, nil); resp.code != 200 {
		t.Fatalf("status with a viewer token: %d", resp.code)
	}
	// A token needs no CSRF token, but a viewer cannot calibrate.
	if resp, _ := viewer.do("POST", api.PathCalibrations, map[string]any{}); resp.code != http.StatusForbidden {
		t.Errorf("a viewer calibrated: %d", resp.code)
	}
	resp, body = anon.do("POST", api.PathSession, api.Login{Token: created.Secret})
	if resp.code != 200 || len(resp.cookies) != 1 || !strings.Contains(string(body), `"role":"viewer"`) {
		t.Fatalf("sign in with a token: %d %s", resp.code, body)
	}
	if resp, _ := anon.do("POST", api.PathSession, api.Login{Token: "greenlens_x"}); resp.code != http.StatusUnauthorized {
		t.Errorf("bad token sign-in: %d", resp.code)
	}
	if resp, _ := sock.do("POST", api.PathTokens, api.CreateToken{Name: "Bad Name", Role: "viewer"}); resp.code != http.StatusBadRequest {
		t.Errorf("bad token name: %d", resp.code)
	}
	resp, body = sock.do("GET", api.PathTokens, nil)
	if resp.code != 200 || strings.Contains(string(body), "argon2") || !strings.Contains(string(body), `"grafana"`) {
		t.Fatalf("token list: %d %s", resp.code, body)
	}
	if resp, _ := sock.do("DELETE", api.PathTokens+"/grafana", nil); resp.code != 200 {
		t.Fatalf("revoke: %d", resp.code)
	}
	if resp, _ := viewer.do("GET", api.PathStatus, nil); resp.code != http.StatusUnauthorized {
		t.Errorf("a revoked token still works: %d", resp.code)
	}

	// Signing out ends the session.
	if resp, _ := browser.do("DELETE", api.PathSession, nil, api.HeaderCSRF, sess.CSRF); resp.code != http.StatusNoContent {
		t.Fatalf("sign out: %d", resp.code)
	}
	if resp, _ := browser.do("GET", api.PathStatus, nil); resp.code != http.StatusUnauthorized {
		t.Errorf("an ended session still works: %d", resp.code)
	}

	resp, body = sock.do("GET", api.PathAudit+"?limit=10", nil)
	var audit api.Audit
	if resp.code != 200 || json.Unmarshal(body, &audit) != nil {
		t.Fatalf("audit: %d %s", resp.code, body)
	}
	actions := map[string]bool{}
	for _, e := range audit.Entries {
		actions[e.Action] = true
		if strings.Contains(e.Detail, created.Secret) {
			t.Error("the audit log holds a secret")
		}
	}
	for _, want := range []string{"sign_in", "token_create", "token_revoke"} {
		if !actions[want] {
			t.Errorf("audit lacks %s: %+v", want, audit.Entries)
		}
	}

	waitSamples(t, r, 3)
	resp, body = anon.do("GET", api.PathMetrics, nil)
	if resp.code != 200 || !strings.HasPrefix(resp.header.Get("Content-Type"), "text/plain; version=0.0.4") {
		t.Fatalf("metrics: %d %v", resp.code, resp.header)
	}
	checkExposition(t, string(body))
	for _, want := range []string{
		`greenlens_node_energy_joules_total{domain="package",source="fake",quality="measured"} `,
		`greenlens_node_power_watts{domain="platform",source="fake",quality="platform"} 3`,
		`greenlens_source_up{source="fake"} 1`,
		`greenlens_collector_duration_seconds_count{collector="source:fake"} `,
		`greenlens_build_info{version="test",`,
		`greenlens_node_unattributed_energy_joules_total `,
		`greenlens_self_energy_joules_total `,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics lack %s", want)
		}
	}

	// One address cannot flood the API.
	limited := false
	for range requestBurst + 20 {
		if resp, _ := anon.do("GET", api.PathHealth, nil); resp.code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("no rate limit")
	}
}

func TestExposedWithoutTokensIsRefused(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Agent.SampleInterval = config.Duration(50 * time.Millisecond)
	cfg.Agent.DataDir = filepath.Join(dir, "data")
	sockDir, err := os.MkdirTemp("", "gl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	cfg.Agent.Socket = filepath.Join(sockDir, "a.sock")
	_, port, _ := net.SplitHostPort(freePort(t))
	cfg.Server.Listen = net.JoinHostPort("0.0.0.0", port)
	run := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return Run(ctx, Options{Config: cfg, Getenv: func(string) string { return "" },
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Sources: []source.Source{&fake{}},
			HostFS: sourcetest.Host(t, "amd-zen4-desktop"), NoSandbox: true})
	}
	if err := run(); help.As(err).Code != help.GL4011 {
		t.Fatalf("started on every interface without tokens: %v", err)
	}
	if _, err := os.Stat(cfg.Agent.Socket); err == nil {
		t.Error("the socket was left behind")
	}
	tokens, err := auth.OpenTokens(filepath.Join(cfg.Agent.DataDir, TokensName))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := tokens.Create("admin", auth.Admin, 0); err != nil {
		t.Fatal(err)
	}
	if err := run(); err != nil {
		t.Fatalf("refused with a token: %v", err)
	}
}

func TestLoginURL(t *testing.T) {
	for _, tt := range []struct {
		sv   config.Server
		want string
	}{
		{config.Server{Listen: "127.0.0.1:10060"}, "http://127.0.0.1:10060/login?code=c"},
		{config.Server{Listen: "0.0.0.0:10060"}, "http://127.0.0.1:10060/login?code=c"},
		{config.Server{Listen: ":10060", TLS: config.TLS{CertFile: "/c", KeyFile: "/k"}}, "https://127.0.0.1:10060/login?code=c"},
		{config.Server{Listen: "[::1]:10060"}, "http://[::1]:10060/login?code=c"},
	} {
		if got := loginURL(tt.sv, "c"); got != tt.want {
			t.Errorf("%+v: %s", tt.sv, got)
		}
	}
	if u, err := url.Parse(loginURL(config.Server{Listen: "127.0.0.1:1"}, "a+b/c")); err != nil || u.Query().Get("code") != "a+b/c" {
		t.Errorf("code not escaped: %v", u)
	}
}

var (
	sampleLine = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{([a-zA-Z_][a-zA-Z0-9_]*="([^"\\]|\\.)*",?)*\})? (\S+)$`)
	metaLine   = regexp.MustCompile(`^# (HELP|TYPE) ([a-zA-Z_:][a-zA-Z0-9_:]*) .+$`)
)

// checkExposition checks the text format: every sample belongs to a family
// declared before it with HELP and TYPE, values parse, and no series
// appears twice.
func checkExposition(t *testing.T, text string) {
	t.Helper()
	declared := map[string]string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if m := metaLine.FindStringSubmatch(line); m != nil {
			if m[1] == "TYPE" {
				declared[m[2]] = strings.Fields(line)[3]
			}
			continue
		}
		m := sampleLine.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("malformed line %q", line)
			continue
		}
		name := m[1]
		base := name
		for _, sfx := range []string{"_bucket", "_sum", "_count"} {
			if b, ok := strings.CutSuffix(name, sfx); ok && declared[b] == "histogram" {
				base = b
			}
		}
		if declared[base] == "" {
			t.Errorf("%s has no TYPE before it", name)
		}
		if _, err := strconv.ParseFloat(strings.TrimPrefix(m[len(m)-1], "+"), 64); err != nil {
			t.Errorf("bad value in %q", line)
		}
		series := strings.TrimSuffix(line, " "+m[len(m)-1])
		if seen[series] {
			t.Errorf("series %s appears twice", series)
		}
		seen[series] = true
	}
}

func TestMetricsCanNeedAToken(t *testing.T) {
	listen := freePort(t)
	startAgentWith(t, "", func(c *config.Config) {
		c.Server.Enabled = true
		c.Server.Listen = listen
		c.Server.MetricsAuth = true
	})
	anon := call{t: t, c: http.DefaultClient, base: "http://" + listen}
	if resp, _ := anon.do("GET", api.PathMetrics, nil); resp.code != http.StatusUnauthorized {
		t.Fatalf("metrics without a token: %d", resp.code)
	}
}
