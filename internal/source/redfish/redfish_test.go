// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package redfish

import (
	"context"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

const token = "session-token"

// bmc serves a set of Redfish resources and asks for the session token.
type bmc struct {
	mu        sync.Mutex
	resources map[string]string
	requests  []string
}

func (b *bmc) set(path, body string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.resources[path] = body
}

func (b *bmc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.requests = append(b.requests, r.URL.Path)
	if r.Header.Get("X-Auth-Token") != token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	body, ok := b.resources[r.URL.Path]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

func start(t *testing.T, resources map[string]string) (*bmc, Options) {
	t.Helper()
	b := &bmc{resources: resources}
	srv := httptest.NewTLSServer(b)
	t.Cleanup(srv.Close)
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	return b, Options{Enabled: true, Endpoint: srv.URL, Token: []byte(token), Roots: roots, Interval: 10 * time.Millisecond}
}

func probe(t *testing.T, o Options) (*Source, source.ProbeResult) {
	t.Helper()
	s := New(o)
	t.Cleanup(func() { _ = s.Close() })
	res, err := s.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s, res
}

func waitPower(t *testing.T, s *Source, want float64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rs, err := s.Read(context.Background())
		if err == nil && len(rs) == 1 && rs[0].PowerW == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("power is %+v (%v), want %v W", rs, err, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func detail(t *testing.T, res source.ProbeResult) string {
	t.Helper()
	if len(res.Channels) != 0 || len(res.Problems) != 1 || res.Problems[0].Code != help.GL1016 {
		t.Fatalf("want one GL1016 and no channel, got %+v", res)
	}
	return help.New(res.Problems[0].Code, res.Problems[0].Args...).Error()
}

func TestEnvironmentMetrics(t *testing.T) {
	b, o := start(t, map[string]string{
		"/redfish/v1/Chassis": `{"Members":[{"@odata.id":"/redfish/v1/Chassis/1U"}]}`,
		"/redfish/v1/Chassis/1U": `{"Id":"1U","EnvironmentMetrics":{"@odata.id":"/redfish/v1/Chassis/1U/EnvironmentMetrics"},
			"Power":{"@odata.id":"/redfish/v1/Chassis/1U/Power"}}`,
		"/redfish/v1/Chassis/1U/EnvironmentMetrics": `{"PowerWatts":{"DataSourceUri":"/redfish/v1/Chassis/1U/Sensors/TotalPower","Reading":374.5}}`,
	})
	s, res := probe(t, o)
	if len(res.Problems) != 0 || len(res.Channels) != 1 {
		t.Fatalf("%+v", res)
	}
	ch := res.Channels[0]
	if ch.ID != "redfish:1U" || ch.Domain != source.DomainPlatform || ch.Quality != source.QualityPlatform ||
		ch.Kind != source.KindPower || ch.Method != source.MethodIntegrated || ch.Instance != "1U" {
		t.Fatalf("channel %+v", ch)
	}
	// The value of the probe is there before the first background read.
	waitPower(t, s, 374.5)
	b.set("/redfish/v1/Chassis/1U/EnvironmentMetrics", `{"PowerWatts":{"Reading":410}}`)
	waitPower(t, s, 410)
	// The deprecated resource is not asked for when the current one works.
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, r := range b.requests {
		if strings.HasSuffix(r, "/Power") {
			t.Fatalf("the Power resource was requested: %v", b.requests)
		}
	}
}

func TestLegacyPowerResource(t *testing.T) {
	_, o := start(t, map[string]string{
		"/redfish/v1/Chassis":           `{"Members":[{"@odata.id":"/redfish/v1/Chassis/Enclosure"},{"@odata.id":"/redfish/v1/Chassis/System.Embedded.1"}]}`,
		"/redfish/v1/Chassis/Enclosure": `{"Id":"Enclosure"}`,
		"/redfish/v1/Chassis/System.Embedded.1": `{"Power":{"@odata.id":"/redfish/v1/Chassis/System.Embedded.1/Power"},
			"EnvironmentMetrics":{"@odata.id":"/redfish/v1/Chassis/System.Embedded.1/EnvironmentMetrics"}}`,
		// This controller links EnvironmentMetrics but reports no power in it.
		"/redfish/v1/Chassis/System.Embedded.1/EnvironmentMetrics": `{"PowerWatts":null}`,
		"/redfish/v1/Chassis/System.Embedded.1/Power":              `{"PowerControl":[{"PowerConsumedWatts":null},{"PowerConsumedWatts":288}]}`,
	})
	s, res := probe(t, o)
	if len(res.Problems) != 0 || len(res.Channels) != 1 || res.Channels[0].ID != "redfish:System.Embedded.1" {
		t.Fatalf("%+v", res)
	}
	waitPower(t, s, 288)
}

func TestConfiguredChassis(t *testing.T) {
	b, o := start(t, map[string]string{
		"/redfish/v1/Chassis/B":       `{"Power":{"@odata.id":"/redfish/v1/Chassis/B/Power"}}`,
		"/redfish/v1/Chassis/B/Power": `{"PowerControl":[{"PowerConsumedWatts":120}]}`,
	})
	o.Chassis = "B"
	s, res := probe(t, o)
	if len(res.Channels) != 1 || res.Channels[0].ID != "redfish:B" {
		t.Fatalf("%+v", res)
	}
	waitPower(t, s, 120)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.requests[0] != "/redfish/v1/Chassis/B" {
		t.Fatalf("the collection was listed although a chassis is configured: %v", b.requests)
	}
}

func TestProblems(t *testing.T) {
	_, o := start(t, map[string]string{
		"/redfish/v1/Chassis":   `{"Members":[{"@odata.id":"/redfish/v1/Chassis/1"}]}`,
		"/redfish/v1/Chassis/1": `{"Id":"1"}`,
	})
	_, res := probe(t, o)
	if d := detail(t, res); !strings.Contains(d, "no chassis reports its power") {
		t.Fatalf("no power: %s", d)
	}

	bad := o
	bad.Token = []byte("expired")
	_, res = probe(t, bad)
	if d := detail(t, res); !strings.Contains(d, "401") || !strings.Contains(d, "session token") {
		t.Fatalf("a refused token: %s", d)
	}

	bad = o
	bad.Roots = x509.NewCertPool()
	_, res = probe(t, bad)
	if d := detail(t, res); !strings.Contains(d, "certificate") {
		t.Fatalf("an unknown authority: %s", d)
	}

	bad = o
	bad.Err = errors.New("the token file cannot be read")
	_, res = probe(t, bad)
	if d := detail(t, res); !strings.Contains(d, "token file") {
		t.Fatalf("an error of the options: %s", d)
	}

	bad = o
	bad.Endpoint = strings.Replace(o.Endpoint, "https://", "http://", 1)
	_, res = probe(t, bad)
	detail(t, res)

	_, empty := start(t, map[string]string{"/redfish/v1/Chassis": `{"Members":[]}`})
	_, res = probe(t, empty)
	if d := detail(t, res); !strings.Contains(d, "lists no chassis") {
		t.Fatalf("no chassis: %s", d)
	}
}

func TestLinksOutsideTheServiceAreRefused(t *testing.T) {
	b, o := start(t, map[string]string{
		"/redfish/v1/Chassis":   `{"Members":[{"@odata.id":"/redfish/v1/Chassis/1"}]}`,
		"/redfish/v1/Chassis/1": `{"EnvironmentMetrics":{"@odata.id":"https://203.0.113.9/redfish/v1/x"},"Power":{"@odata.id":"/redfish/v1/../admin"}}`,
	})
	_, res := probe(t, o)
	if d := detail(t, res); !strings.Contains(d, "unexpected resource path") {
		t.Fatalf("%s", d)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.requests) != 2 {
		t.Fatalf("requests beyond the collection and the chassis: %v", b.requests)
	}
}

func TestDisabledMakesNoRequest(t *testing.T) {
	b, o := start(t, nil)
	o.Enabled = false
	s, res := probe(t, o)
	if len(res.Channels) != 0 || len(res.Problems) != 0 {
		t.Fatalf("%+v", res)
	}
	if rs, err := s.Read(context.Background()); rs != nil || err != nil {
		t.Fatalf("%v %v", rs, err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.requests) != 0 {
		t.Fatalf("requests while disabled: %v", b.requests)
	}
}

func TestReadStopsWhenTheControllerStopsAnswering(t *testing.T) {
	b, o := start(t, map[string]string{
		"/redfish/v1/Chassis":         `{"Members":[{"@odata.id":"/redfish/v1/Chassis/1"}]}`,
		"/redfish/v1/Chassis/1":       `{"Power":{"@odata.id":"/redfish/v1/Chassis/1/Power"}}`,
		"/redfish/v1/Chassis/1/Power": `{"PowerControl":[{"PowerConsumedWatts":200}]}`,
	})
	s, _ := probe(t, o)
	waitPower(t, s, 200)
	b.set("/redfish/v1/Chassis/1/Power", `{"PowerControl":[]}`)
	deadline := time.Now().Add(5 * time.Second)
	for {
		rs, err := s.Read(context.Background())
		if len(rs) == 0 && err != nil && strings.Contains(err.Error(), "redfish:1") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a stale value is still returned: %+v %v", rs, err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCheckEndpoint(t *testing.T) {
	if err := CheckEndpoint("https://10.0.0.5"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"http://10.0.0.5", "https://203.0.113.9", "https://10.0.0.5/redfish/v1", "10.0.0.5", ""} {
		if err := CheckEndpoint(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}
