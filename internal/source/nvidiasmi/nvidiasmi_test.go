// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package nvidiasmi

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"testing/fstest"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

// output is what nvidia-smi 595.99.02 printed for the query on a machine
// with an RTX 3050, plus a card without power management.
const output = `00000000:01:00.0, 17.03, NVIDIA GeForce RTX 3050
00000000:02:00.0, [N/A], NVIDIA T400
`

func host() fstest.MapFS {
	return fstest.MapFS{
		"sys/bus/pci/devices/0000:01:00.0/uevent": {Data: []byte("DRIVER=nvidia\nPCI_CLASS=30000\n")},
		"sys/bus/pci/devices/0000:02:00.0/uevent": {Data: []byte("DRIVER=nvidia\nPCI_CLASS=30200\n")},
	}
}

// fake writes an nvidia-smi that prints out once; in loop mode it then
// waits to be killed, so its last line ages.
func fake(t *testing.T, out string, exit int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), Command)
	script := `#!/bin/sh
printf '%s' '` + out + `'
case "$*" in
  *--loop-ms=1000*) exec sleep 30 ;;
esac
exit ` + strconv.Itoa(exit) + `
`
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil { //nolint:gosec // the program must be executable
		t.Fatal(err)
	}
	return p
}

func TestParse(t *testing.T) {
	pci, w, name, ok := parse("00000000:01:00.0, 17.03, NVIDIA GeForce RTX 3050, rev 2\n")
	if !ok || pci != "0000:01:00.0" || w != 17.03 || name != "NVIDIA GeForce RTX 3050, rev 2" {
		t.Fatalf("%q %v %q %v", pci, w, name, ok)
	}
	for _, l := range []string{"", "00000000:01:00.0, [Not Supported], x", "bogus, 1, x", "00000000:01:00.0, -3, x"} {
		if _, _, _, ok := parse(l); ok {
			t.Errorf("parse(%q) succeeded", l)
		}
	}
}

func TestProbeAndStream(t *testing.T) {
	s := New(host(), fake(t, output, 0))
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	res, err := s.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Channels) != 1 || len(res.Problems) != 0 {
		t.Fatalf("probe %+v", res)
	}
	c := res.Channels[0]
	if c.ID != "nvidia_smi:0000:01:00.0" || c.Kind != source.KindPower || c.Method != source.MethodIntegrated || c.Domain != source.DomainGPU || c.Label != "NVIDIA GeForce RTX 3050" {
		t.Fatalf("channel %+v", c)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		rs, err := s.Read(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(rs) == 1 {
			if rs[0].PowerW != 17.03 {
				t.Fatalf("reading %+v", rs[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no line from the loop")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The loop printed one line and stopped; a minute later the value is
	// too old to use.
	s.mu.Lock()
	s.now = func() time.Time { return time.Now().Add(time.Minute) }
	s.mu.Unlock()
	if rs, err := s.Read(context.Background()); err != nil || len(rs) != 0 {
		t.Fatalf("stale value used: %+v, %v", rs, err)
	}
}

func TestLoopThatExits(t *testing.T) {
	s := New(host(), fake(t, output, 0))
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Without loop mode the program prints once and exits, as nvidia-smi
	// does when the driver goes away.
	s.mu.Lock()
	s.path = fake(t, output, 0)
	s.mu.Unlock()
	if err := os.WriteFile(s.path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { //nolint:gosec // the program must be executable
		t.Fatal(err)
	}
	if _, err := s.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := s.Read(context.Background())
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the end of the loop was not reported")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestFailingProgram(t *testing.T) {
	res, err := New(host(), fake(t, "NVIDIA-SMI has failed", 9)).Probe(context.Background())
	if err != nil || res.Available() || len(res.Problems) != 1 || res.Problems[0].Code != help.GL1015 {
		t.Fatalf("probe %+v, %v", res, err)
	}
}

func TestNoCardOrNoProgram(t *testing.T) {
	res, err := New(fstest.MapFS{}, fake(t, output, 0)).Probe(context.Background())
	if err != nil || res.Available() || len(res.Problems) != 0 {
		t.Fatalf("without NVIDIA cards: %+v, %v", res, err)
	}
	t.Setenv("PATH", t.TempDir())
	res, err = New(host(), "").Probe(context.Background())
	if err != nil || res.Available() || len(res.Problems) != 0 {
		t.Fatalf("without nvidia-smi: %+v, %v", res, err)
	}
}
