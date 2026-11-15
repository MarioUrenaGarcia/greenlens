// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package wallmeter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

// meter answers requests by path and query, with the documents the
// documentation of each device shows.
type meter struct {
	mu       sync.Mutex
	answers  map[string]string
	status   map[string]int
	requests []string
}

func (m *meter) set(key, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.answers[key] = body
}

func (m *meter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := r.URL.Path
	if r.URL.RawQuery != "" {
		key += "?" + r.URL.RawQuery
	}
	m.requests = append(m.requests, key)
	if code, ok := m.status[key]; ok {
		w.WriteHeader(code)
		return
	}
	body, ok := m.answers[key]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_, _ = w.Write([]byte(body))
}

func start(t *testing.T, kind string, answers map[string]string) (*meter, Options) {
	t.Helper()
	m := &meter{answers: answers, status: map[string]int{}}
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	return m, Options{Enabled: true, Kind: kind, Endpoint: srv.URL, Interval: 10 * time.Millisecond}
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
	if len(res.Channels) != 0 || len(res.Problems) != 1 || res.Problems[0].Code != help.GL1017 {
		t.Fatalf("want one GL1017 and no channel, got %+v", res)
	}
	return help.New(res.Problems[0].Code, res.Problems[0].Args...).Error()
}

const (
	shellyGen2 = `{"name":null,"id":"shellyplusplugs-000000000000","mac":"000000000000","model":"SNPL-00112EU","gen":2,"fw_id":"20230913-113421/1.0.3-g0ff4ac8","ver":"1.0.3","app":"PlusPlugS","auth_en":false,"auth_domain":null}`
	shellyGen1 = `{"type":"SHPLG-S","mac":"000000000000","auth":false,"fw":"20230913-113421/v1.14.0-gcb84623","longid":1,"num_outputs":1,"num_meters":1}`
)

func TestShellyOfTheSecondGeneration(t *testing.T) {
	m, o := start(t, KindShelly, map[string]string{
		"/shelly":                    shellyGen2,
		"/rpc/Switch.GetStatus?id=0": `{"id":0,"source":"init","output":true,"apower":142.7,"voltage":127.3,"current":1.142,"aenergy":{"total":1830.512,"by_minute":[2378.3,2380.1,2377.9],"minute_ts":1700000040},"temperature":{"tC":38.2,"tF":100.8}}`,
	})
	s, res := probe(t, o)
	if len(res.Problems) != 0 || len(res.Channels) != 1 {
		t.Fatalf("%+v", res)
	}
	ch := res.Channels[0]
	if ch.ID != "wall_meter:0" || ch.Domain != source.DomainWall || ch.Quality != source.QualityPlatform ||
		ch.Kind != source.KindPower || ch.Method != source.MethodIntegrated || ch.Label != "Shelly output 0" {
		t.Fatalf("channel %+v", ch)
	}
	waitPower(t, s, 142.7)
	m.set("/rpc/Switch.GetStatus?id=0", `{"id":0,"output":true,"apower":96.1}`)
	waitPower(t, s, 96.1)
}

func TestShellyOfTheFirstGeneration(t *testing.T) {
	_, o := start(t, KindShelly, map[string]string{
		"/shelly":  shellyGen1,
		"/meter/1": `{"power":77.25,"overpower":0,"is_valid":true,"timestamp":1700000000,"counters":[77.1,77.3,77.2],"total":120340}`,
	})
	o.Channel = 1
	s, res := probe(t, o)
	if len(res.Channels) != 1 || res.Channels[0].ID != "wall_meter:1" {
		t.Fatalf("%+v", res)
	}
	waitPower(t, s, 77.25)
}

func TestTasmota(t *testing.T) {
	status := "/cm?cmnd=Status+10"
	m, o := start(t, KindTasmota, map[string]string{
		status: `{"StatusSNS":{"Time":"2026-01-01T00:00:00","ENERGY":{"TotalStartTime":"2025-01-01T00:00:00","Total":12.345,"Yesterday":1.2,"Today":0.4,"Power":61,"ApparentPower":70,"ReactivePower":34,"Factor":0.87,"Voltage":127,"Current":0.551}}}`,
	})
	s, res := probe(t, o)
	if len(res.Problems) != 0 || len(res.Channels) != 1 || res.Channels[0].Label != "Tasmota output 0" {
		t.Fatalf("%+v", res)
	}
	waitPower(t, s, 61)

	// A device with two outputs reports one power per output.
	m.set(status, `{"StatusSNS":{"ENERGY":{"Power":[30.5,12]}}}`)
	two := o
	two.Channel = 1
	s2, res := probe(t, two)
	if len(res.Channels) != 1 || res.Channels[0].ID != "wall_meter:1" {
		t.Fatalf("%+v", res)
	}
	waitPower(t, s2, 12)
	two.Channel = 2
	_, res = probe(t, two)
	if d := detail(t, res); !strings.Contains(d, "2 outputs") {
		t.Fatalf("a missing output: %s", d)
	}
}

func TestProblems(t *testing.T) {
	_, o := start(t, KindShelly, map[string]string{
		"/shelly":                    shellyGen2,
		"/rpc/Switch.GetStatus?id=0": `{"id":0,"output":true}`,
	})
	_, res := probe(t, o)
	if d := detail(t, res); !strings.Contains(d, "may not measure") {
		t.Fatalf("a plug without a meter: %s", d)
	}

	_, locked := start(t, KindShelly, map[string]string{"/shelly": strings.Replace(shellyGen2, `"auth_en":false`, `"auth_en":true`, 1)})
	_, res = probe(t, locked)
	if d := detail(t, res); !strings.Contains(d, "password") {
		t.Fatalf("a locked Shelly: %s", d)
	}

	_, tasmota := start(t, KindTasmota, map[string]string{"/cm?cmnd=Status+10": `{"WARNING":"Need user=<username>&password=<password>"}`})
	_, res = probe(t, tasmota)
	if d := detail(t, res); !strings.Contains(d, "password") {
		t.Fatalf("a locked Tasmota: %s", d)
	}

	_, plain := start(t, KindTasmota, map[string]string{"/cm?cmnd=Status+10": `{"StatusSNS":{"Time":"2026-01-01T00:00:00"}}`})
	_, res = probe(t, plain)
	if d := detail(t, res); !strings.Contains(d, "ENERGY") {
		t.Fatalf("a device without a meter: %s", d)
	}

	// Asking a Tasmota device as if it were a Shelly.
	_, wrong := start(t, KindShelly, map[string]string{"/shelly": "<html>not found</html>"})
	_, res = probe(t, wrong)
	if d := detail(t, res); !strings.Contains(d, "sources.wall_meter.kind") {
		t.Fatalf("the wrong kind: %s", d)
	}

	for _, bad := range []Options{
		{Enabled: true, Kind: KindShelly, Endpoint: "http://203.0.113.9", Interval: time.Second},
		{Enabled: true, Kind: KindShelly, Endpoint: o.Endpoint, Channel: MaxChannel + 1, Interval: time.Second},
		{Enabled: true, Kind: "kasa", Endpoint: o.Endpoint, Interval: time.Second},
	} {
		_, res = probe(t, bad)
		detail(t, res)
	}
}

func TestNegativePowerIsNotAReading(t *testing.T) {
	_, o := start(t, KindShelly, map[string]string{
		"/shelly":                    shellyGen2,
		"/rpc/Switch.GetStatus?id=0": `{"id":0,"apower":-3.2}`,
	})
	_, res := probe(t, o)
	if d := detail(t, res); !strings.Contains(d, "-3.2 W") {
		t.Fatalf("%s", d)
	}
}

func TestDisabledMakesNoRequest(t *testing.T) {
	m, o := start(t, KindShelly, nil)
	o.Enabled = false
	s, res := probe(t, o)
	if len(res.Channels) != 0 || len(res.Problems) != 0 {
		t.Fatalf("%+v", res)
	}
	if rs, err := s.Read(context.Background()); rs != nil || err != nil {
		t.Fatalf("%v %v", rs, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requests) != 0 {
		t.Fatalf("requests while disabled: %v", m.requests)
	}
}

func TestCheckEndpoint(t *testing.T) {
	if err := CheckEndpoint("http://192.168.1.50"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"https://192.168.1.50", "http://203.0.113.9", "http://192.168.1.50/status", "192.168.1.50", ""} {
		if err := CheckEndpoint(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}
