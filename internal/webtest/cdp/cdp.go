// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package cdp drives a headless Chromium through the DevTools protocol over
// a pipe, for the end-to-end and accessibility tests of the interface. The
// pipe needs no network port, and the protocol is plain JSON, so no
// third-party code is involved.
package cdp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
)

// Candidates are the names Chromium is installed under.
var Candidates = []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"}

// Find returns the browser named by GREENLENS_CHROME, or the first of
// Candidates on the PATH.
func Find() (string, error) {
	if p := os.Getenv("GREENLENS_CHROME"); p != "" {
		return exec.LookPath(p)
	}
	for _, name := range Candidates {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no Chromium found; install one of %s or set GREENLENS_CHROME", strings.Join(Candidates, ", "))
}

// flags keep the browser from reaching the network on its own: no updates,
// no sync, no safe browsing lookups, no metrics.
var flags = []string{
	"--headless=new", "--remote-debugging-pipe", "--no-first-run", "--no-default-browser-check",
	"--disable-background-networking", "--disable-component-update", "--disable-sync",
	"--disable-extensions", "--disable-default-apps", "--disable-domain-reliability",
	"--disable-client-side-phishing-detection", "--no-pings", "--mute-audio",
	"--metrics-recording-only", "--password-store=basic", "--use-mock-keychain",
}

// Error is an error answered by the browser.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("cdp error %d: %s", e.Code, e.Message) }

type message struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *Error          `json:"error,omitempty"`
}

// Event is a notification of the browser.
type Event struct {
	Method string
	Params json.RawMessage
}

type subscription struct {
	session, method string
	ch              chan Event
}

// Browser is a running Chromium.
type Browser struct {
	cmd  *exec.Cmd
	w    io.WriteCloser
	r    io.ReadCloser
	wmu  sync.Mutex
	mu   sync.Mutex
	next int64
	wait map[int64]chan message
	subs []*subscription
	done chan struct{}
	err  error
}

// Launch starts the browser at path with its profile in dir.
func Launch(path, dir string, extra ...string) (*Browser, error) {
	// The browser reads commands from its descriptor 3 and writes answers
	// to its descriptor 4.
	cmdR, cmdW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	args := append(append(append([]string{}, flags...), "--user-data-dir="+dir), extra...)
	args = append(args, "about:blank")
	cmd := exec.Command(path, args...) //nolint:gosec,noctx // the browser of the tests; it is closed through the protocol
	cmd.ExtraFiles = []*os.File{cmdR, outW}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	_ = cmdR.Close()
	_ = outW.Close()
	b := &Browser{cmd: cmd, w: cmdW, r: outR, wait: map[int64]chan message{}, done: make(chan struct{})}
	go b.read()
	return b, nil
}

func (b *Browser) read() {
	rd := bufio.NewReaderSize(b.r, 1<<20)
	var err error
	for {
		var raw []byte
		raw, err = rd.ReadBytes(0)
		if err != nil {
			break
		}
		var m message
		if err = json.Unmarshal(bytes.TrimSuffix(raw, []byte{0}), &m); err != nil {
			break
		}
		b.mu.Lock()
		if m.ID != 0 {
			if ch, ok := b.wait[m.ID]; ok {
				delete(b.wait, m.ID)
				ch <- m
			}
		} else {
			for _, s := range b.subs {
				if s.method == m.Method && s.session == m.SessionID {
					select {
					case s.ch <- Event{Method: m.Method, Params: m.Params}:
					default:
						// A test that stops reading must not stop the
						// browser connection.
					}
				}
			}
		}
		b.mu.Unlock()
	}
	b.mu.Lock()
	b.err = err
	for id, ch := range b.wait {
		close(ch)
		delete(b.wait, id)
	}
	b.mu.Unlock()
	close(b.done)
}

// Call sends a command, within a session when session is not empty, and
// decodes its result into out unless out is nil.
func (b *Browser) Call(ctx context.Context, session, method string, params, out any) error {
	b.mu.Lock()
	b.next++
	id := b.next
	ch := make(chan message, 1)
	b.wait[id] = ch
	b.mu.Unlock()
	m := message{ID: id, Method: method, SessionID: session}
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return err
		}
		m.Params = p
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	b.wmu.Lock()
	_, err = b.w.Write(append(data, 0))
	b.wmu.Unlock()
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return fmt.Errorf("%s: browser connection closed: %w", method, b.err)
		}
		if r.Error != nil {
			return fmt.Errorf("%s: %w", method, r.Error)
		}
		if out != nil && len(r.Result) > 0 {
			return json.Unmarshal(r.Result, out)
		}
		return nil
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.wait, id)
		b.mu.Unlock()
		return fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

// Subscribe returns the events of one method in a session, kept in a
// buffer of n, and the function that ends the subscription.
func (b *Browser) Subscribe(session, method string, n int) (<-chan Event, func()) {
	s := &subscription{session: session, method: method, ch: make(chan Event, n)}
	b.mu.Lock()
	b.subs = append(b.subs, s)
	b.mu.Unlock()
	return s.ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.subs = slices.DeleteFunc(b.subs, func(x *subscription) bool { return x == s })
	}
}

// Close asks the browser to quit and waits for it, killing it when it does
// not quit in time.
func (b *Browser) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = b.Call(ctx, "", "Browser.close", nil, nil)
	exited := make(chan error, 1)
	go func() { exited <- b.cmd.Wait() }()
	var err error
	select {
	case err = <-exited:
	case <-time.After(10 * time.Second):
		_ = b.cmd.Process.Kill()
		err = <-exited
	}
	_ = b.w.Close()
	_ = b.r.Close()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		// Browser.close ends the process before it answers; how it exits
		// then says nothing about the tests.
		return nil
	}
	return err
}

// Page is a tab of the browser.
type Page struct {
	B       *Browser
	Target  string
	Session string
}

// NewPage opens a blank tab and attaches to it.
func (b *Browser) NewPage(ctx context.Context) (*Page, error) {
	var created struct {
		TargetID string `json:"targetId"`
	}
	if err := b.Call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &created); err != nil {
		return nil, err
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := b.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": created.TargetID, "flatten": true}, &attached); err != nil {
		return nil, err
	}
	p := &Page{B: b, Target: created.TargetID, Session: attached.SessionID}
	for _, domain := range []string{"Page", "Runtime", "Network"} {
		if err := p.Call(ctx, domain+".enable", nil, nil); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// Call sends a command to the page.
func (p *Page) Call(ctx context.Context, method string, params, out any) error {
	return p.B.Call(ctx, p.Session, method, params, out)
}

// Subscribe returns the events of one method of the page and the function
// that ends the subscription.
func (p *Page) Subscribe(method string, n int) (<-chan Event, func()) {
	return p.B.Subscribe(p.Session, method, n)
}

// Navigate loads url and waits for its load event.
func (p *Page) Navigate(ctx context.Context, url string) error {
	loaded, stop := p.Subscribe("Page.loadEventFired", 4)
	defer stop()
	var nav struct {
		LoaderID  string `json:"loaderId"`
		ErrorText string `json:"errorText"`
	}
	if err := p.Call(ctx, "Page.navigate", map[string]any{"url": url}, &nav); err != nil {
		return err
	}
	if nav.ErrorText != "" {
		return fmt.Errorf("navigate to %s: %s", url, nav.ErrorText)
	}
	if nav.LoaderID == "" {
		// Only the fragment changed: the document stays and fires no load
		// event.
		return nil
	}
	select {
	case <-loaded:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("navigate to %s: %w", url, ctx.Err())
	}
}

// Reload reloads the page and waits for its load event.
func (p *Page) Reload(ctx context.Context) error {
	loaded, stop := p.Subscribe("Page.loadEventFired", 4)
	defer stop()
	if err := p.Call(ctx, "Page.reload", map[string]any{"ignoreCache": false}, nil); err != nil {
		return err
	}
	select {
	case <-loaded:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("reload: %w", ctx.Err())
	}
}

// Eval evaluates a JavaScript expression, waiting for it when it is a
// promise, and decodes its value into out unless out is nil.
func (p *Page) Eval(ctx context.Context, expr string, out any) error {
	var res struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	err := p.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression": expr, "awaitPromise": true, "returnByValue": true, "userGesture": true,
	}, &res)
	if err != nil {
		return err
	}
	if d := res.ExceptionDetails; d != nil {
		return fmt.Errorf("%s %s", d.Text, d.Exception.Description)
	}
	if out != nil && len(res.Result.Value) > 0 {
		return json.Unmarshal(res.Result.Value, out)
	}
	return nil
}

// WaitFor evaluates a boolean expression until it is true.
func (p *Page) WaitFor(ctx context.Context, expr string) error {
	for {
		var ok bool
		if err := p.Eval(ctx, "Boolean("+expr+")", &ok); err == nil && ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s: %w", expr, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Keys with the codes that Chromium needs to act on them.
var keys = map[string]struct {
	code string
	vk   int
	text string
}{
	"Tab": {"Tab", 9, ""}, "Enter": {"Enter", 13, "\r"}, "Escape": {"Escape", 27, ""},
	"Space": {"Space", 32, " "}, "ArrowDown": {"ArrowDown", 40, ""}, "ArrowUp": {"ArrowUp", 38, ""},
	"ArrowLeft": {"ArrowLeft", 37, ""}, "ArrowRight": {"ArrowRight", 39, ""},
}

// Shift is the modifier of Key for Shift.
const Shift = 8

// Key presses and releases a key, such as Tab or Enter, with modifiers.
func (p *Page) Key(ctx context.Context, name string, modifiers int) error {
	k, ok := keys[name]
	if !ok {
		return fmt.Errorf("unknown key %q", name)
	}
	key := name
	if name == "Space" {
		key = " "
	}
	down := map[string]any{"type": "keyDown", "key": key, "code": k.code, "windowsVirtualKeyCode": k.vk, "modifiers": modifiers}
	if k.text != "" {
		down["text"] = k.text
	}
	if err := p.Call(ctx, "Input.dispatchKeyEvent", down, nil); err != nil {
		return err
	}
	return p.Call(ctx, "Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": key, "code": k.code, "windowsVirtualKeyCode": k.vk, "modifiers": modifiers}, nil)
}

// Type inserts text in the focused element, as typing it would.
func (p *Page) Type(ctx context.Context, text string) error {
	return p.Call(ctx, "Input.insertText", map[string]any{"text": text}, nil)
}

// AXNode is a node of the accessibility tree.
type AXNode struct {
	NodeID  string `json:"nodeId"`
	Ignored bool   `json:"ignored"`
	Role    *struct {
		Value string `json:"value"`
	} `json:"role"`
	Name *struct {
		Value string `json:"value"`
	} `json:"name"`
	BackendDOMNodeID int `json:"backendDOMNodeId"`
}

// RoleName returns the role and the accessible name of the node.
func (n AXNode) RoleName() (string, string) {
	var role, name string
	if n.Role != nil {
		role = n.Role.Value
	}
	if n.Name != nil {
		name = n.Name.Value
	}
	return role, name
}

// AXTree returns the full accessibility tree of the page.
func (p *Page) AXTree(ctx context.Context) ([]AXNode, error) {
	var res struct {
		Nodes []AXNode `json:"nodes"`
	}
	err := p.Call(ctx, "Accessibility.getFullAXTree", nil, &res)
	return res.Nodes, err
}
