// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/auth"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/web"
)

// Limits of the HTTP API.
const (
	maxHeaderBytes = 32 << 10
	// requestRate and requestBurst bound the requests of one address.
	requestRate  = 30
	requestBurst = 60
	// loginRate bounds the sign-in attempts with a token of one address,
	// per minute.
	loginRate = 10
)

// csp allows nothing but the files of the interface itself.
const csp = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; " +
	"connect-src 'self'; manifest-src 'self'; worker-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// startHTTP opens the HTTP listener. It refuses to listen beyond loopback
// while no access token exists, since anyone on the network could then read
// the measurements.
func (a *Agent) startHTTP(ctx context.Context) (net.Listener, error) {
	a.mu.RLock()
	sv := a.cfg.Server
	a.mu.RUnlock()
	if !sv.Enabled {
		return nil, nil
	}
	if !config.IsLoopback(sv.Listen) && a.tokens.Len() == 0 {
		return nil, help.New(help.GL4011, "listen", sv.Listen)
	}
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "tcp", sv.Listen)
	if err != nil {
		return nil, help.New(help.GL4012, "listen", sv.Listen, "detail", err.Error()).Wrap(err)
	}
	if sv.TLS.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(sv.TLS.CertFile, sv.TLS.KeyFile)
		if err != nil {
			_ = l.Close()
			return nil, help.New(help.GL4014, "detail", err.Error()).Wrap(err)
		}
		l = tls.NewListener(l, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	}
	return l, nil
}

// httpServer serves the API, the interface and the metrics on l.
func (a *Agent) httpServer() *http.Server {
	return &http.Server{
		Handler:           a.httpHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    maxHeaderBytes,
	}
}

// httpHandler wraps the routes with the protections of a network listener:
// security headers, rate limits, a bound on bodies, authentication and a
// CSRF check for browser sessions.
func (a *Agent) httpHandler() http.Handler {
	mux := a.mux(false)
	mux.Handle("GET /", newStatic(web.Assets()))
	a.mu.RLock()
	metricsAuth := a.cfg.Server.MetricsAuth
	a.mu.RUnlock()
	for _, rt := range a.httpRoutes(metricsAuth) {
		mux.Handle(rt.pattern, require(rt.role, rt.h))
	}
	requests := newLimiter(requestRate, requestBurst)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), usb=(), serial=(), bluetooth=()")
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		if !requests.allow(clientIP(r)) {
			h.Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, "too many requests; slow down")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		p, ok, err := a.authenticate(r)
		if err != nil {
			a.limited("session", "reading a session failed", "error", err)
			writeError(w, http.StatusInternalServerError, "session check failed")
			return
		}
		if ok {
			if p.Via == auth.ViaSession && !safeMethod(r.Method) && !sameOrigin(r, p.CSRF) {
				writeError(w, http.StatusForbidden, "missing or wrong CSRF token")
				return
			}
			r = r.WithContext(auth.WithPrincipal(r.Context(), p))
		}
		mux.ServeHTTP(w, r)
	})
}

// httpRoutes are served only on the HTTP listener: signing in and out,
// and the metrics, public unless metricsAuth is set. The socket serves the
// metrics without conditions.
func (a *Agent) httpRoutes(metricsAuth bool) []route {
	var metricsRole auth.Role
	if metricsAuth {
		metricsRole = auth.Viewer
	}
	return []route{
		{pattern: "GET " + api.PathLogin, h: a.handleLogin},
		{pattern: "POST " + api.PathSession, h: a.handleTokenLogin},
		{pattern: "DELETE " + api.PathSession, h: a.handleLogout},
		{pattern: "GET " + api.PathMetrics, role: metricsRole, h: a.handleMetrics},
	}
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sameOrigin checks a request of a browser session that changes something:
// it must carry the CSRF token of the session and must not come from
// another site.
func sameOrigin(r *http.Request, csrf string) bool {
	got := r.Header.Get(api.HeaderCSRF)
	if csrf == "" || subtle.ConstantTimeCompare([]byte(got), []byte(csrf)) != 1 {
		return false
	}
	if s := r.Header.Get("Sec-Fetch-Site"); s != "" && s != "same-origin" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || u.Host != r.Host {
			return false
		}
	}
	return true
}

// authenticate recognizes a bearer token or a session cookie. A request
// with neither has no principal; one with a bad token is anonymous too, so
// it gets the same answer as a request without credentials.
func (a *Agent) authenticate(r *http.Request) (auth.Principal, bool, error) {
	if h := r.Header.Get("Authorization"); h != "" {
		secret, ok := strings.CutPrefix(h, "Bearer ")
		if !ok {
			return auth.Principal{}, false, nil
		}
		tk, ok := a.tokens.Verify(strings.TrimSpace(secret))
		if !ok {
			return auth.Principal{}, false, nil
		}
		return auth.Principal{Role: tk.Role, Actor: "token:" + tk.Name, Via: auth.ViaToken}, true, nil
	}
	c, err := r.Cookie(api.SessionCookie)
	if err != nil {
		return auth.Principal{}, false, nil
	}
	sess, ok, err := a.sessions.Lookup(r.Context(), c.Value)
	if err != nil || !ok {
		return auth.Principal{}, false, err
	}
	return auth.Principal{Role: sess.Role, Actor: sess.Actor, Via: auth.ViaSession, CSRF: sess.CSRF}, true, nil
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// setSession starts a browser session and sets its cookie.
func (a *Agent) setSession(w http.ResponseWriter, r *http.Request, role auth.Role, actor string) (auth.Session, error) {
	id, sess, err := a.sessions.Create(r.Context(), role, actor)
	if err != nil {
		return auth.Session{}, err
	}
	// Secure follows TLS: without it the API serves loopback or an SSH
	// tunnel, where a Secure cookie would never be sent back.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // HttpOnly and SameSite are set; Secure follows TLS as explained above
		Name: api.SessionCookie, Value: id, Path: "/", MaxAge: int(auth.SessionLifetime.Seconds()),
		HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode,
	})
	return sess, nil
}

// handleLogin redeems a one-time code from greenlens gui and opens the
// interface with a new session.
func (a *Agent) handleLogin(w http.ResponseWriter, r *http.Request) {
	role, who, ok := a.codes.Redeem(r.URL.Query().Get("code"))
	if !ok {
		http.Redirect(w, r, "/#/login?expired=1", http.StatusSeeOther)
		return
	}
	if _, err := a.setSession(w, r, role, who); err != nil {
		a.log.Error("session not created", "error", err)
		writeError(w, http.StatusInternalServerError, "session not created")
		return
	}
	if err := a.st.Audit(r.Context(), who, "sign_in", "one-time link, role "+string(role)); err != nil {
		a.log.Warn("audit log write failed", "error", err)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleTokenLogin opens a session for a valid access token, for browsers
// on other machines where greenlens gui cannot be run.
func (a *Agent) handleTokenLogin(w http.ResponseWriter, r *http.Request) {
	if !a.logins.allow(clientIP(r)) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "too many sign-in attempts; wait a minute")
		return
	}
	var body api.Login
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "the body must be JSON with a token")
		return
	}
	tk, ok := a.tokens.Verify(strings.TrimSpace(body.Token))
	if !ok {
		writeError(w, http.StatusUnauthorized, "the token is not valid")
		return
	}
	who := "token:" + tk.Name
	sess, err := a.setSession(w, r, tk.Role, who)
	if err != nil {
		a.log.Error("session not created", "error", err)
		writeError(w, http.StatusInternalServerError, "session not created")
		return
	}
	if err := a.st.Audit(r.Context(), who, "sign_in", "token, role "+string(tk.Role)); err != nil {
		a.log.Warn("audit log write failed", "error", err)
	}
	writeJSON(w, http.StatusOK, api.Session{SchemaVersion: api.SchemaVersion, Role: string(sess.Role), Actor: who, Via: auth.ViaSession, CSRF: sess.CSRF})
}

// handleSession says who the request comes from, and gives a browser
// session its CSRF token.
func (a *Agent) handleSession(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "sign in or send an access token")
		return
	}
	writeJSON(w, http.StatusOK, api.Session{SchemaVersion: api.SchemaVersion, Role: string(p.Role), Actor: p.Actor, Via: p.Via, CSRF: p.CSRF})
}

func (a *Agent) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(api.SessionCookie); err == nil {
		if err := a.sessions.End(r.Context(), c.Value); err != nil {
			a.log.Warn("session not ended", "error", err)
		}
	}
	// The attributes match setSession, so the browser replaces that cookie.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows TLS, as in setSession
		Name: api.SessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleSessionCode gives a local user a one-time link to the interface.
// It is served only on the socket, whose clients are trusted.
func (a *Agent) handleSessionCode(w http.ResponseWriter, r *http.Request) {
	var body api.SessionCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "the body must be JSON")
		return
	}
	role := auth.Admin
	if body.Role != "" {
		var err error
		if role, err = auth.ParseRole(body.Role); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	a.mu.RLock()
	sv := a.cfg.Server
	a.mu.RUnlock()
	if !sv.Enabled {
		writeError(w, http.StatusConflict, "the HTTP API is disabled; set server.enabled")
		return
	}
	c, err := a.codes.Issue(role, actor(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "code not created")
		return
	}
	writeJSON(w, http.StatusOK, api.SessionCode{SchemaVersion: api.SchemaVersion, URL: loginURL(sv, c), ExpiresAt: time.Now().Add(auth.CodeLifetime).UTC()})
}

// loginURL is the address a browser on this machine opens to redeem a code.
// An agent listening on every interface is reached through loopback.
func loginURL(sv config.Server, code string) string {
	host, port, _ := net.SplitHostPort(sv.Listen)
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	scheme := "http"
	if sv.TLS.CertFile != "" {
		scheme = "https"
	}
	u := url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port), Path: api.PathLogin, RawQuery: url.Values{"code": {code}}.Encode()}
	return u.String()
}

func (a *Agent) handleTokens(w http.ResponseWriter, _ *http.Request) {
	out := api.Tokens{SchemaVersion: api.SchemaVersion, Tokens: []api.Token{}}
	for _, tk := range a.tokens.List() {
		out.Tokens = append(out.Tokens, apiToken(tk))
	}
	writeJSON(w, http.StatusOK, out)
}

func apiToken(tk auth.Token) api.Token {
	return api.Token{ID: tk.ID, Name: tk.Name, Role: string(tk.Role), CreatedAt: tk.CreatedAt, ExpiresAt: tk.ExpiresAt}
}

// maxTokenLifetime bounds the expiry of a token to ten years.
const maxTokenLifetime = 10 * 366 * 24 * time.Hour

func (a *Agent) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var body api.CreateToken
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "the body must be JSON with a name and a role")
		return
	}
	role, err := auth.ParseRole(body.Role)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ttl := time.Duration(body.ExpiresInS) * time.Second
	if body.ExpiresInS < 0 || ttl > maxTokenLifetime {
		writeError(w, http.StatusBadRequest, "expires_in_s must be between 0 and ten years")
		return
	}
	secret, tk, err := a.tokens.Create(body.Name, role, ttl)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.st.Audit(r.Context(), actor(r), "token_create", fmt.Sprintf("%s (%s), role %s", tk.Name, tk.ID, tk.Role)); err != nil {
		a.log.Warn("audit log write failed", "error", err)
	}
	writeJSON(w, http.StatusCreated, api.CreatedToken{SchemaVersion: api.SchemaVersion, Token: apiToken(tk), Secret: secret})
}

func (a *Agent) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	tk, err := a.tokens.Revoke(r.PathValue("id"))
	if errors.Is(err, auth.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no token with this id or name")
		return
	}
	if err != nil {
		a.log.Error("token not revoked", "error", err)
		writeError(w, http.StatusInternalServerError, "token not revoked")
		return
	}
	if err := a.st.Audit(r.Context(), actor(r), "token_revoke", fmt.Sprintf("%s (%s)", tk.Name, tk.ID)); err != nil {
		a.log.Warn("audit log write failed", "error", err)
	}
	writeJSON(w, http.StatusOK, apiToken(tk))
}

func (a *Agent) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 1000")
			return
		}
		limit = n
	}
	var before int64
	if v := r.URL.Query().Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "before must be the id of an entry")
			return
		}
		before = n
	}
	entries, err := a.st.AuditLog(r.Context(), limit, before)
	if err != nil {
		a.log.Error("audit query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "audit query failed")
		return
	}
	writeJSON(w, http.StatusOK, api.Audit{SchemaVersion: api.SchemaVersion, Entries: entries})
}

// limiter is a token bucket per client address.
type limiter struct {
	rate, burst float64

	mu      sync.Mutex
	buckets map[string]*bucket
	swept   time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(perSecond, burst float64) *limiter {
	return &limiter{rate: perSecond, burst: burst, buckets: map[string]*bucket{}}
}

func (l *limiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.swept) > time.Minute {
		// Buckets that refilled completely carry no state worth keeping.
		for k, b := range l.buckets {
			if b.tokens+now.Sub(b.last).Seconds()*l.rate >= l.burst {
				delete(l.buckets, k)
			}
		}
		l.swept = now
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
