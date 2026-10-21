// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

//go:build e2e

package cdp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDrivesTheBrowser(t *testing.T) {
	path, err := Find()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><html lang="en"><title>t</title><button id="b" onclick="this.textContent='done'">Go</button><input id="i" aria-label="Name">`))
	}))
	defer srv.Close()
	b, err := Launch(path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, err := b.NewPage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Navigate(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := p.Eval(ctx, "document.title", &title); err != nil || title != "t" {
		t.Fatalf("title %q %v", title, err)
	}
	// Tab reaches the button; Enter presses it.
	if err := p.Key(ctx, "Tab", 0); err != nil {
		t.Fatal(err)
	}
	if err := p.Key(ctx, "Enter", 0); err != nil {
		t.Fatal(err)
	}
	if err := p.WaitFor(ctx, `document.getElementById("b").textContent === "done"`); err != nil {
		t.Fatal(err)
	}
	if err := p.Key(ctx, "Tab", 0); err != nil {
		t.Fatal(err)
	}
	if err := p.Type(ctx, "abc"); err != nil {
		t.Fatal(err)
	}
	var v string
	if err := p.Eval(ctx, `document.getElementById("i").value`, &v); err != nil || v != "abc" {
		t.Fatalf("typed %q %v", v, err)
	}
	nodes, err := p.AXTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range nodes {
		if role, name := n.RoleName(); role == "textbox" && name == "Name" {
			found = true
		}
	}
	if !found {
		t.Fatalf("textbox Name not in the tree of %d nodes", len(nodes))
	}
	if err := p.Eval(ctx, "Promise.reject(new Error('boom'))", nil); err == nil {
		t.Fatal("a rejected promise did not fail")
	}
}
