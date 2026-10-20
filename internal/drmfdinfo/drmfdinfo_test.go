// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package drmfdinfo

import (
	"fmt"
	"math"
	"testing"
	"testing/fstest"
	"time"
)

// amdgpu prints the busy time of each IP block (amdgpu_fdinfo.c); this
// client of Xwayland was recorded on a Raphael APU before it submitted
// work, so it has no engine lines.
const recordedAMD = "pos:\t0\nflags:\t02100002\nmnt_id:\t41\nino:\t1284\ndrm-driver:\tamdgpu\ndrm-client-id:\t212\ndrm-pdev:\t0000:0d:00.0\npasid:\t73\ndrm-total-cpu:\t0\ndrm-total-gtt:\t2 MiB\ndrm-memory-gtt: \t2048 KiB\n"

func amd(id int, gfx, compute uint64) string {
	return fmt.Sprintf("pos:\t0\ndrm-driver:\tamdgpu\ndrm-client-id:\t%d\ndrm-pdev:\t0000:03:00.0\ndrm-engine-gfx:\t%d ns\ndrm-engine-compute:\t%d ns\n", id, gfx, compute)
}

// xe prints busy and total cycles per engine class (xe_drm_client.c).
func xe(cycles, total uint64) string {
	return fmt.Sprintf("drm-driver:\txe\ndrm-client-id:\t3\ndrm-pdev:\t0000:04:00.0\ndrm-cycles-rcs:\t%d\ndrm-total-cycles-rcs:\t%d\ndrm-cycles-ccs:\t%d\ndrm-total-cycles-ccs:\t%d\ndrm-engine-capacity-ccs:\t4\n", cycles, total, 4*cycles, total)
}

func TestParse(t *testing.T) {
	c, ok := Parse([]byte(recordedAMD))
	if !ok || c.Driver != "amdgpu" || c.PDev != "0000:0d:00.0" || c.ID != 212 || len(c.EngineNS) != 0 {
		t.Fatalf("%+v %v", c, ok)
	}
	c, ok = Parse([]byte(amd(7, 1500, 20)))
	if !ok || c.EngineNS["gfx"] != 1500 || c.EngineNS["compute"] != 20 {
		t.Fatalf("%+v", c)
	}
	c, ok = Parse([]byte(xe(10, 100)))
	if !ok || c.Cycles["rcs"] != 10 || c.TotalCycles["ccs"] != 100 || c.Capacity["ccs"] != 4 {
		t.Fatalf("%+v", c)
	}
	for _, s := range []string{"pos:\t0\nflags:\t02\n", "drm-driver:\tamdgpu\ndrm-client-id:\t1\n", "drm-driver:\tamdgpu\ndrm-pdev:\t0000:03:00.0\n"} {
		if _, ok := Parse([]byte(s)); ok {
			t.Errorf("not a usable client: %q", s)
		}
	}
}

func TestUse(t *testing.T) {
	fsys := fstest.MapFS{
		"proc/10/fdinfo/0": {Data: []byte("pos:\t0\nflags:\t02\n")},
		"proc/10/fdinfo/7": {Data: []byte(amd(1, 0, 0))},
		"proc/11/fdinfo/9": {Data: []byte(amd(2, 0, 0))},
		"proc/12/fdinfo/3": {Data: []byte(xe(0, 1000))},
		"proc/13/fdinfo/4": {Data: []byte(amd(1, 0, 0))}, // shares client 1 with pid 10
		"proc/14/fdinfo/1": {Data: []byte(recordedAMD)},
	}
	at := time.Unix(1000, 0)
	r := New(fsys)
	r.now = func() time.Time { return at }
	r.Scan([]int{10, 11, 12, 13, 14, 99})
	first := r.Use()
	if u := first["0000:03:00.0"]; len(u.PIDs) != 2 || u.Busy != nil {
		t.Fatalf("first read has no interval yet: %+v", first)
	}
	// One second later pid 10 kept the graphics engine busy half the time
	// and pid 11 a quarter; on xe, rcs ran 10% of the cycles and the four
	// compute engines 40% of theirs together.
	at = at.Add(time.Second)
	fsys["proc/10/fdinfo/7"] = &fstest.MapFile{Data: []byte(amd(1, 500_000_000, 0))}
	fsys["proc/11/fdinfo/9"] = &fstest.MapFile{Data: []byte(amd(2, 200_000_000, 50_000_000))}
	fsys["proc/12/fdinfo/3"] = &fstest.MapFile{Data: []byte(xe(100, 2000))}
	use := r.Use()
	a := use["0000:03:00.0"]
	if math.Abs(a.Busy[10]-50) > 1e-9 || math.Abs(a.Busy[11]-25) > 1e-9 || a.Busy[13] != 0 {
		t.Fatalf("amdgpu %+v", a)
	}
	if x := use["0000:04:00.0"]; math.Abs(x.Busy[12]-20) > 1e-9 {
		t.Fatalf("xe %+v", x)
	}
	if u := use["0000:0d:00.0"]; len(u.PIDs) != 1 || u.Busy != nil {
		t.Fatalf("idle client %+v", u)
	}
	// A counter that goes down counts as not moving, and then from its
	// highest value.
	at = at.Add(time.Second)
	fsys["proc/10/fdinfo/7"] = &fstest.MapFile{Data: []byte(amd(1, 100, 0))}
	if b := r.Use()["0000:03:00.0"].Busy[10]; b != 0 {
		t.Fatalf("busy %v", b)
	}
	at = at.Add(time.Second)
	fsys["proc/10/fdinfo/7"] = &fstest.MapFile{Data: []byte(amd(1, 600_000_000, 0))}
	if b := r.Use()["0000:03:00.0"].Busy[10]; math.Abs(b-10) > 1e-9 {
		t.Fatalf("busy %v after catching up", b)
	}
}
