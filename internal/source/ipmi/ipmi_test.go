// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package ipmi

import (
	"context"
	"io/fs"
	"testing"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
)

// response builds a DCMI Get Power Reading response for watts.
func response(cc byte, watts uint16, active bool) []byte {
	r := make([]byte, responseLen)
	r[0] = cc
	r[1] = dcmiGroupID
	r[2], r[3] = byte(watts&0xff), byte(watts>>8)
	if active {
		r[18] = stateActive
	}
	return r
}

type fakeDevice struct {
	resp   []byte
	req    []byte
	netfn  byte
	cmd    byte
	closed bool
}

func (d *fakeDevice) Request(netfn, cmd byte, data []byte) ([]byte, error) {
	d.netfn, d.cmd, d.req = netfn, cmd, data
	return d.resp, nil
}
func (d *fakeDevice) Close() error { d.closed = true; return nil }

type fakeOpener struct {
	dev  *fakeDevice
	err  error
	path string
}

func (o *fakeOpener) Open(p string) (Device, error) {
	if o.err != nil {
		return nil, o.err
	}
	o.path = p
	return o.dev, nil
}

func TestParsePowerReading(t *testing.T) {
	w, active, err := parsePowerReading(response(0, 0x0123, true))
	if err != nil || !active || w != 291 {
		t.Fatalf("got %v %v %v", w, active, err)
	}
	if _, _, err := parsePowerReading(response(0xC1, 10, true)); err == nil {
		t.Error("non-zero completion code accepted")
	}
	if _, _, err := parsePowerReading(response(0, 10, true)[:10]); err == nil {
		t.Error("short response accepted")
	}
	bad := response(0, 10, true)
	bad[1] = 0
	if _, _, err := parsePowerReading(bad); err == nil {
		t.Error("wrong group id accepted")
	}
	if _, _, err := parsePowerReading(nil); err == nil {
		t.Error("empty response accepted")
	}
}

func TestProbeAndRead(t *testing.T) {
	dev := &fakeDevice{resp: response(0, 412, true)}
	o := &fakeOpener{dev: dev}
	s := New("/host", o)
	res, err := s.Probe(context.Background())
	if err != nil || !res.Available() {
		t.Fatalf("result %+v, err %v", res, err)
	}
	if o.path != "/host/dev/ipmi0" || dev.netfn != 0x2C || dev.cmd != 0x02 || len(dev.req) != 4 || dev.req[0] != 0xDC || dev.req[1] != 1 {
		t.Fatalf("request path %s netfn %#x cmd %#x data %x", o.path, dev.netfn, dev.cmd, dev.req)
	}
	rs, err := s.Read(context.Background())
	if err != nil || len(rs) != 1 || rs[0].PowerW != 412 {
		t.Fatalf("readings %+v, err %v", rs, err)
	}
	dev.resp = response(0, 412, false)
	if rs, err := s.Read(context.Background()); err != nil || len(rs) != 0 {
		t.Fatalf("inactive measurement must give no value: %+v %v", rs, err)
	}
	if err := s.Close(); err != nil || !dev.closed {
		t.Fatal("device not closed")
	}
}

func TestProbeProblems(t *testing.T) {
	res, err := New("/", &fakeOpener{err: fs.ErrPermission}).Probe(context.Background())
	if err != nil || res.Available() || len(res.Problems) != 1 || res.Problems[0].Code != help.GL1004 {
		t.Fatalf("permission: %+v %v", res, err)
	}
	res, err = New("/", &fakeOpener{err: fs.ErrNotExist}).Probe(context.Background())
	if err != nil || res.Available() || len(res.Problems) != 0 {
		t.Fatalf("missing device: %+v %v", res, err)
	}
	dev := &fakeDevice{resp: response(0, 0, false)}
	res, err = New("/", &fakeOpener{dev: dev}).Probe(context.Background())
	if err != nil || res.Available() || len(res.Problems) != 1 || res.Problems[0].Code != help.GL1006 || !dev.closed {
		t.Fatalf("inactive BMC: %+v %v", res, err)
	}
}
