// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package nvml

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate testdata")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "testdata", "nvml", name)
}

// fakeHelper writes a helper that answers the n-th request with the n-th
// line of a recorded file, using only shell builtins because the helper
// runs with an empty environment.
func fakeHelper(t *testing.T, recorded string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), HelperName)
	script := `#!/bin/sh
i=0
while IFS= read -r req; do
  i=$((i+1)); j=0
  while IFS= read -r line; do
    j=$((j+1))
    if [ "$j" -eq "$i" ]; then printf '%s\n' "$line"; break; fi
  done < '` + recorded + `'
done
`
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil { //nolint:gosec // the helper must be executable
		t.Fatal(err)
	}
	return p
}

func host(pcis ...string) fstest.MapFS {
	fsys := fstest.MapFS{
		// The integrated GPU of an AMD processor is not an NVIDIA card.
		"sys/bus/pci/devices/0000:0d:00.0/uevent": {Data: []byte("DRIVER=amdgpu\nPCI_CLASS=30000\n")},
		// An NVIDIA audio function is not a display controller.
		"sys/bus/pci/devices/0000:01:00.1/uevent": {Data: []byte("DRIVER=snd_hda_intel\nPCI_CLASS=40300\n")},
	}
	for _, p := range pcis {
		fsys["sys/bus/pci/devices/"+p+"/uevent"] = &fstest.MapFile{Data: []byte("DRIVER=nvidia\nPCI_CLASS=30200\n")}
	}
	return fsys
}

func TestNVIDIADevices(t *testing.T) {
	got, err := NVIDIADevices(host("0000:01:00.0", "0000:3B:00.0"))
	if err != nil || strings.Join(got, ",") != "0000:01:00.0,0000:3b:00.0" {
		t.Fatalf("%v, %v", got, err)
	}
	fsys := host()
	fsys["sys/bus/pci/devices/0000:02:00.0/uevent"] = &fstest.MapFile{Data: []byte("DRIVER=nouveau\nPCI_CLASS=30000\n")}
	if got, err := NVIDIADevices(fsys); err != nil || len(got) != 0 {
		t.Fatalf("a card on nouveau has no NVML: %v, %v", got, err)
	}
	if got, err := NVIDIADevices(fstest.MapFS{}); err != nil || len(got) != 0 {
		t.Fatalf("no PCI bus: %v, %v", got, err)
	}
}

func probeRead(t *testing.T, s *Source, reads int) (source.ProbeResult, [][]source.Reading) {
	t.Helper()
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	res, err := s.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out [][]source.Reading
	for range reads {
		rs, err := s.Read(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, rs)
	}
	return res, out
}

func TestRecordedRTX3050(t *testing.T) {
	s := New(host("0000:01:00.0"), fakeHelper(t, fixture(t, "driver-595.jsonl")))
	res, reads := probeRead(t, s, 2)
	if len(res.Channels) != 1 || len(res.Problems) != 0 {
		t.Fatalf("probe %+v", res)
	}
	c := res.Channels[0]
	if c.ID != "nvml:0000:01:00.0" || c.Domain != source.DomainGPU || c.Instance != "0000:01:00.0" || c.Kind != source.KindCounter || c.Method != source.MethodMeasured || c.Label != "NVIDIA GeForce RTX 3050" {
		t.Fatalf("channel %+v", c)
	}
	if s.Driver() != "595.99.02" {
		t.Errorf("driver %q", s.Driver())
	}
	// 135438030 - 135417960 mJ.
	if d := reads[1][0].EnergyJ - reads[0][0].EnergyJ; math.Abs(d-20.07) > 1e-9 {
		t.Errorf("delta %v J", d)
	}
	u := s.Usage()["0000:01:00.0"]
	if u.Util != 30 || len(u.PIDs) != 3 || len(u.Processes) != 1 || u.Processes[0].PID != 1002 || u.Processes[0].SM != 33 {
		t.Errorf("usage %+v", u)
	}
}

func TestPowerOnlyAndEnergyCards(t *testing.T) {
	s := New(host("0000:3b:00.0", "0000:d8:00.0"), fakeHelper(t, fixture(t, "driver-470-two-gpus.jsonl")))
	res, reads := probeRead(t, s, 1)
	if len(res.Channels) != 2 {
		t.Fatalf("probe %+v", res)
	}
	kinds := map[string]source.Kind{}
	for _, c := range res.Channels {
		kinds[c.ID] = c.Kind
	}
	if kinds["nvml:0000:3b:00.0"] != source.KindPower || kinds["nvml:0000:d8:00.0"] != source.KindCounter {
		t.Fatalf("kinds %v", kinds)
	}
	got := map[string]source.Reading{}
	for _, r := range reads[0] {
		got[r.Channel] = r
	}
	if got["nvml:0000:3b:00.0"].PowerW != 31.25 || got["nvml:0000:d8:00.0"].EnergyJ != 4200 {
		t.Errorf("readings %+v", got)
	}
}

func TestNoNVIDIACardStartsNothing(t *testing.T) {
	s := New(host(), "/nonexistent/"+HelperName)
	res, reads := probeRead(t, s, 1)
	if res.Available() || len(res.Problems) != 0 || len(reads[0]) != 0 || s.proc != nil {
		t.Fatalf("probe %+v", res)
	}
}

func TestMissingHelper(t *testing.T) {
	s := New(host("0000:01:00.0"), filepath.Join(t.TempDir(), HelperName))
	res, _ := probeRead(t, s, 0)
	if res.Available() || len(res.Problems) != 1 || res.Problems[0].Code != help.GL1013 {
		t.Fatalf("probe %+v", res)
	}
}

func TestHelperErrors(t *testing.T) {
	dir := t.TempDir()
	for name, lines := range map[string]string{
		"driver": `{"version":1,"error":{"call":"nvmlInit_v2","return":9,"text":"Driver Not Loaded"}}`,
		"proto":  `{"version":99}`,
		"junk":   `this is not json`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := filepath.Join(dir, name+".jsonl")
			if err := os.WriteFile(rec, []byte(lines+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			s := New(host("0000:01:00.0"), fakeHelper(t, rec))
			res, _ := probeRead(t, s, 0)
			if res.Available() || len(res.Problems) != 1 || res.Problems[0].Code != help.GL1014 {
				t.Fatalf("probe %+v", res)
			}
		})
	}
}

func TestHelperThatDies(t *testing.T) {
	// This helper answers the probe and exits.
	p := filepath.Join(t.TempDir(), HelperName)
	script := "#!/bin/sh\nIFS= read -r req\nprintf '%s\\n' '" + `{"version":1,"devices":[{"index":0,"name":"x","pci":"0000:01:00.0","energy":true}]}` + "'\n"
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil { //nolint:gosec // the helper must be executable
		t.Fatal(err)
	}
	s := New(host("0000:01:00.0"), p)
	t.Cleanup(func() { _ = s.Close() })
	if res, err := s.Probe(context.Background()); err != nil || !res.Available() {
		t.Fatalf("probe %+v, %v", res, err)
	}
	if _, err := s.Read(context.Background()); err == nil {
		t.Fatal("reading from a helper that exited must fail")
	}
	if s.proc != nil {
		t.Error("the dead helper is kept")
	}
	// Without a helper the channels have no value and no error repeats.
	if rs, err := s.Read(context.Background()); err != nil || len(rs) != 0 {
		t.Errorf("%+v, %v", rs, err)
	}
}

func TestGPUUse(t *testing.T) {
	s := New(host("0000:01:00.0"), fakeHelper(t, fixture(t, "driver-595.jsonl")))
	probeRead(t, s, 1)
	var _ source.GPUUser = s
	u := s.GPUUse()["0000:01:00.0"]
	if len(u.PIDs) != 3 || u.Busy[1002] != 31 || len(u.Busy) != 1 {
		t.Fatalf("%+v", u)
	}
}
