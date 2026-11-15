// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package otlp

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/exporter/prometheus"
)

// field is one decoded protobuf field.
type field struct {
	num  int
	wire int
	val  uint64
	data []byte
}

// decode reads the fields of a message; it fails the test on anything
// that is not well formed, so every nested message is checked too.
func decode(t *testing.T, b []byte) []field {
	t.Helper()
	var out []field
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			t.Fatalf("bad tag in % x", b)
		}
		b = b[n:]
		f := field{num: int(tag >> 3), wire: int(tag & 7)}
		switch f.wire {
		case wireVarint:
			v, n := binary.Uvarint(b)
			if n <= 0 {
				t.Fatal("bad varint")
			}
			f.val, b = v, b[n:]
		case wireFixed64:
			if len(b) < 8 {
				t.Fatal("short fixed64")
			}
			f.val, b = binary.LittleEndian.Uint64(b), b[8:]
		case wireBytes:
			l, n := binary.Uvarint(b)
			if n <= 0 || l > math.MaxInt32 || len(b)-n < int(l) {
				t.Fatal("bad length")
			}
			end := n + int(l) //nolint:gosec // bounded by the check above
			f.data, b = b[n:end], b[end:]
		default:
			t.Fatalf("unexpected wire type %d", f.wire)
		}
		out = append(out, f)
	}
	return out
}

// get returns the fields with a number.
func get(fs []field, num int) []field {
	var out []field
	for _, f := range fs {
		if f.num == num {
			out = append(out, f)
		}
	}
	return out
}

// one returns the only field with a number.
func one(t *testing.T, fs []field, num int) field {
	t.Helper()
	got := get(fs, num)
	if len(got) != 1 {
		t.Fatalf("field %d appears %d times", num, len(got))
	}
	return got[0]
}

func attrs(t *testing.T, fs []field, num int) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, kv := range get(fs, num) {
		in := decode(t, kv.data)
		val := decode(t, one(t, in, fKeyValueValue).data)
		out[string(one(t, in, fKeyValueKey).data)] = string(one(t, val, fAnyValueString).data)
	}
	return out
}

func fixed64s(t *testing.T, b []byte) []uint64 {
	t.Helper()
	if len(b)%8 != 0 {
		t.Fatalf("packed field of %d bytes", len(b))
	}
	var out []uint64
	for ; len(b) > 0; b = b[8:] {
		out = append(out, binary.LittleEndian.Uint64(b))
	}
	return out
}

func families() []prometheus.Family {
	L := prometheus.L
	c := prometheus.NewCollectors()
	c.Observe("rapl", 2*time.Millisecond, nil)
	c.Observe("rapl", 20*time.Millisecond, io.ErrUnexpectedEOF)
	c.Observe("rapl", 7*time.Second, nil)
	return append([]prometheus.Family{
		{Name: "greenlens_node_energy_joules_total", Help: "Energy.", Type: prometheus.Counter, Samples: []prometheus.Sample{
			{Labels: L("domain", "package", "source", "rapl", "quality", "measured"), Value: 1234.5},
			{Labels: L("domain", "gpu", "source", "nvml", "quality", "measured"), Value: 88},
		}},
		{Name: "greenlens_node_power_watts", Help: "Power.", Type: prometheus.Gauge, Samples: []prometheus.Sample{
			{Labels: L("domain", "package", "source", "", "quality", "measured"), Value: 41.25},
		}},
		{Name: "greenlens_energy_cost_total", Help: "Cost.", Type: prometheus.Counter},
	}, c.Families()...)
}

func TestEncode(t *testing.T) {
	start, now := time.Unix(1_800_000_000, 0), time.Unix(1_800_000_060, 500)
	res := Resource{Attributes: prometheus.L("service.name", "greenlens", "host.name", "node-a"), Version: "v1.2.3"}
	req := decode(t, Encode(families(), res, start, now))
	rm := decode(t, one(t, req, fRequestResourceMetrics).data)
	if got := attrs(t, decode(t, one(t, rm, fResourceMetricsResource).data), fResourceAttributes); got["service.name"] != "greenlens" || got["host.name"] != "node-a" || len(got) != 2 {
		t.Fatalf("resource %v", got)
	}
	sm := decode(t, one(t, rm, fResourceMetricsScope).data)
	scope := decode(t, one(t, sm, fScopeMetricsScope).data)
	if string(one(t, scope, fScopeName).data) != ScopeName || string(one(t, scope, fScopeVersion).data) != "v1.2.3" {
		t.Fatalf("scope %v", scope)
	}
	metrics := map[string][]field{}
	var names []string
	for _, m := range get(sm, fScopeMetricsMetrics) {
		fs := decode(t, m.data)
		name := string(one(t, fs, fMetricName).data)
		metrics[name] = fs
		names = append(names, name)
	}
	// The family without samples is left out.
	want := []string{"greenlens_node_energy_joules_total", "greenlens_node_power_watts", "greenlens_collector_errors_total", "greenlens_collector_duration_seconds"}
	if !slices.Equal(names, want) {
		t.Fatalf("metrics %v", names)
	}

	energy := metrics["greenlens_node_energy_joules_total"]
	if string(one(t, energy, fMetricUnit).data) != "J" || string(one(t, energy, fMetricDescription).data) != "Energy." {
		t.Fatalf("energy %v", energy)
	}
	sum := decode(t, one(t, energy, fMetricSum).data)
	if one(t, sum, fTemporality).val != temporalityCumulative || one(t, sum, fSumIsMonotonic).val != 1 {
		t.Fatalf("sum %v", sum)
	}
	points := get(sum, fDataPoints)
	if len(points) != 2 {
		t.Fatalf("%d points", len(points))
	}
	p := decode(t, points[0].data)
	if one(t, p, fPointStart).val != uint64(start.UnixNano()) || one(t, p, fPointTime).val != uint64(now.UnixNano()) ||
		math.Float64frombits(one(t, p, fNumberAsDouble).val) != 1234.5 {
		t.Fatalf("point %v", p)
	}
	if got := attrs(t, p, fNumberAttributes); got["domain"] != "package" || got["source"] != "rapl" || got["quality"] != "measured" {
		t.Fatalf("attributes %v", got)
	}

	power := metrics["greenlens_node_power_watts"]
	gauge := decode(t, one(t, power, fMetricGauge).data)
	gp := decode(t, one(t, gauge, fDataPoints).data)
	if len(get(gp, fPointStart)) != 0 || math.Float64frombits(one(t, gp, fNumberAsDouble).val) != 41.25 {
		t.Fatalf("gauge point %v", gp)
	}
	// A label with an empty value stays an empty string.
	if got := attrs(t, gp, fNumberAttributes); len(got) != 3 || got["source"] != "" {
		t.Fatalf("gauge attributes %v", got)
	}

	dur := metrics["greenlens_collector_duration_seconds"]
	if string(one(t, dur, fMetricUnit).data) != "s" {
		t.Fatalf("duration %v", dur)
	}
	hist := decode(t, one(t, dur, fMetricHistogram).data)
	if one(t, hist, fTemporality).val != temporalityCumulative {
		t.Fatalf("histogram %v", hist)
	}
	hp := decode(t, one(t, hist, fDataPoints).data)
	bounds := fixed64s(t, one(t, hp, fHistBounds).data)
	buckets := fixed64s(t, one(t, hp, fHistBucketCounts).data)
	if len(bounds) != len(prometheus.Buckets) || len(buckets) != len(bounds)+1 {
		t.Fatalf("%d bounds, %d buckets", len(bounds), len(buckets))
	}
	for i, b := range bounds {
		if math.Float64frombits(b) != prometheus.Buckets[i] {
			t.Fatalf("bound %d is %v", i, math.Float64frombits(b))
		}
	}
	// 2 ms falls in (0.001, 0.005], 20 ms in (0.01, 0.05] and 7 s above
	// the last bound: each bucket holds its own observations.
	if !slices.Equal(buckets, []uint64{0, 1, 0, 1, 0, 0, 0, 0, 1}) {
		t.Fatalf("buckets %v", buckets)
	}
	if one(t, hp, fHistCount).val != 3 || math.Abs(math.Float64frombits(one(t, hp, fHistSum).val)-7.022) > 1e-9 {
		t.Fatalf("histogram point %v", hp)
	}
	if got := attrs(t, hp, fHistAttributes); len(got) != 1 || got["collector"] != "rapl" {
		t.Fatalf("histogram attributes %v", got)
	}
}

func TestUnit(t *testing.T) {
	for name, want := range map[string]string{
		"greenlens_node_energy_joules_total":              "J",
		"greenlens_node_idle_power_watts":                 "W",
		"greenlens_model_joules_per_token":                "J/{token}",
		"greenlens_model_tokens_total":                    "{token}",
		"greenlens_emissions_grams_co2e_total":            "g",
		"greenlens_sci_grams_co2e_per_unit":               "g/{unit}",
		"greenlens_water_liters_total":                    "L",
		"greenlens_collector_duration_seconds":            "s",
		"greenlens_collector_errors_total":                "{error}",
		"greenlens_build_info":                            "1",
		"greenlens_source_up":                             "1",
		"greenlens_energy_cost_total":                     "",
		"greenlens_alerts_firing":                         "",
		"greenlens_node_unattributed_energy_joules_total": "J",
	} {
		if got := Unit(name); got != want {
			t.Errorf("Unit(%s) = %q, want %q", name, got, want)
		}
	}
}

func TestExportHTTP(t *testing.T) {
	payload := Encode(families(), Resource{}, time.Unix(1, 0), time.Unix(2, 0))
	var got []byte
	var path, content, auth string
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, content, auth = r.URL.Path, r.Header.Get("Content-Type"), r.Header.Get("Authorization")
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(status)
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL, ProtocolHTTP, []byte("secret"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Export(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	if path != "/v1/metrics" || content != "application/x-protobuf" || auth != "Bearer secret" || !bytes.Equal(got, payload) {
		t.Fatalf("%s %s %s, %d bytes", path, content, auth, len(got))
	}
	// A path of the endpoint is kept, and no token sends no credential.
	c2, err := NewClient(srv.URL+"/otlp/metrics", ProtocolHTTP, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if err := c2.Export(context.Background(), payload); err != nil || path != "/otlp/metrics" || auth != "" {
		t.Fatalf("%s %q %v", path, auth, err)
	}
	status = http.StatusServiceUnavailable
	if err := c.Export(context.Background(), payload); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("a failed export: %v", err)
	}
}

// grpcServer speaks HTTP/2 without TLS, as a collector on loopback does.
func grpcServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.Config.Protocols = new(http.Protocols)
	srv.Config.Protocols.SetUnencryptedHTTP2(true)
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func TestExportGRPC(t *testing.T) {
	payload := Encode(families(), Resource{}, time.Unix(1, 0), time.Unix(2, 0))
	var got []byte
	var path, content, te string
	var proto int
	grpcCode, grpcMsg := "0", ""
	srv := grpcServer(t, func(w http.ResponseWriter, r *http.Request) {
		path, content, te, proto = r.URL.Path, r.Header.Get("Content-Type"), r.Header.Get("Te"), r.ProtoMajor
		got, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
		// An empty ExportMetricsServiceResponse, framed.
		_, _ = w.Write([]byte{0, 0, 0, 0, 0})
		w.Header().Set("Grpc-Status", grpcCode)
		w.Header().Set("Grpc-Message", grpcMsg)
	})
	c, err := NewClient(srv.URL, ProtocolGRPC, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Export(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	if proto != 2 || path != grpcMethod || content != "application/grpc" || te != "trailers" {
		t.Fatalf("HTTP/%d %s %s %q", proto, path, content, te)
	}
	if len(got) != 5+len(payload) || got[0] != 0 || int(binary.BigEndian.Uint32(got[1:5])) != len(payload) || !bytes.Equal(got[5:], payload) {
		t.Fatalf("frame of %d bytes for a request of %d", len(got), len(payload))
	}
	grpcCode, grpcMsg = "7", "permission%20denied"
	if err := c.Export(context.Background(), payload); err == nil || !strings.Contains(err.Error(), "status 7: permission denied") {
		t.Fatalf("a refused export: %v", err)
	}
}

func TestExportGRPCWithoutBody(t *testing.T) {
	// A server that fails before answering sends the status in the
	// headers.
	srv := grpcServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Grpc-Status", "12")
		w.Header().Set("Grpc-Message", "unknown service")
	})
	c, err := NewClient(srv.URL, ProtocolGRPC, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Export(context.Background(), []byte{1}); err == nil || !strings.Contains(err.Error(), "status 12: unknown service") {
		t.Fatalf("%v", err)
	}
	// The HTTP port of a collector answers gRPC with plain HTTP.
	plain := grpcServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	c2, err := NewClient(plain.URL, ProtocolGRPC, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if err := c2.Export(context.Background(), []byte{1}); err == nil || !strings.Contains(err.Error(), "not gRPC") {
		t.Fatalf("%v", err)
	}
}

func TestNewClientErrors(t *testing.T) {
	for _, tc := range []struct{ endpoint, protocol string }{
		{"http://192.0.2.10:4318", ProtocolHTTP},
		{"collector:4317", ProtocolGRPC},
		{"http://127.0.0.1:4317/v1/metrics", ProtocolGRPC},
		{"http://127.0.0.1:4318", "thrift"},
	} {
		if _, err := NewClient(tc.endpoint, tc.protocol, nil, nil); err == nil {
			t.Errorf("%s over %s was accepted", tc.endpoint, tc.protocol)
		}
	}
	if err := CheckEndpoint("http://127.0.0.1:4318"); err != nil {
		t.Fatal(err)
	}
	if err := CheckEndpoint("http://203.0.113.9:4318"); err == nil {
		t.Fatal("an outside collector was accepted")
	}
}
