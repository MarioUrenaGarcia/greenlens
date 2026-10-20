// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package nvml reads NVIDIA GPUs through greenlens-nvml, the helper that
// loads the NVIDIA Management Library, so this binary stays static. The
// helper is started on the first probe and lives until Close; it is not
// started at all on machines without an NVIDIA GPU.
package nvml

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/nvmlproto"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

// ID is the source ID.
const ID = "nvml"

// HelperName is the file name of the helper.
const HelperName = "greenlens-nvml"

// SystemHelperDirs are searched after the directory of the running binary.
var SystemHelperDirs = []string{"/usr/libexec/greenlens", "/usr/lib/greenlens"}

// timeout bounds every exchange with the helper. NVML answers in
// milliseconds; the first probe also loads the library.
const timeout = 5 * time.Second

// maxResponse bounds a response line.
const maxResponse = 1 << 20

// Usage is the use of one GPU in the interval that ended with the last read.
type Usage struct {
	// Util is the percentage of time a kernel ran on the GPU.
	Util uint32
	// PIDs are the processes with a context on the GPU.
	PIDs []int
	// Processes report their share of the streaming multiprocessors.
	Processes []nvmlproto.Process
}

// Source reads NVIDIA GPUs.
type Source struct {
	fsys   fs.FS
	helper string

	mu      sync.Mutex
	proc    *helperProc
	devices map[int]source.Channel
	usage   map[string]Usage
	driver  string
}

// New returns the NVML source. helper is the path of greenlens-nvml; when
// empty it is looked up next to the running binary and then in
// SystemHelperDirs.
func New(fsys fs.FS, helper string) *Source {
	return &Source{fsys: fsys, helper: helper}
}

// ID implements source.Source.
func (s *Source) ID() string { return ID }

// Priority implements source.Source.
func (s *Source) Priority() int { return 1 }

// Driver returns the version of the NVIDIA driver found by the last probe.
func (s *Source) Driver() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.driver
}

// Usage returns the use of every GPU, by PCI address, measured by the last
// read.
func (s *Source) Usage() map[string]Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Usage, len(s.usage))
	for k, v := range s.usage {
		out[k] = v
	}
	return out
}

// GPUUse implements source.GPUUser with the streaming multiprocessor use of
// each process.
func (s *Source) GPUUse() map[string]source.GPUUse {
	out := map[string]source.GPUUse{}
	for inst, u := range s.Usage() {
		g := source.GPUUse{PIDs: u.PIDs}
		if len(u.Processes) > 0 {
			g.Busy = map[int]float64{}
			for _, p := range u.Processes {
				g.Busy[p.PID] = float64(p.SM)
			}
		}
		out[inst] = g
	}
	return out
}

// Probe implements source.Source.
func (s *Source) Probe(ctx context.Context) (source.ProbeResult, error) {
	res := source.ProbeResult{Source: ID}
	gpus, err := NVIDIADevices(s.fsys)
	if err != nil {
		return res, err
	}
	if len(gpus) == 0 {
		return res, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.devices = nil
	if s.proc == nil {
		path, err := s.findHelper()
		if err != nil {
			res.Problems = append(res.Problems, source.Problem{Code: help.GL1013, Args: []any{"dirs", s.searched()}})
			return res, nil
		}
		if s.proc, err = startHelper(path); err != nil {
			res.Problems = append(res.Problems, source.Problem{Code: help.GL1014, Args: []any{"detail", err.Error()}})
			return res, nil
		}
	}
	var resp nvmlproto.ProbeResponse
	if err := s.proc.exchange(ctx, nvmlproto.OpProbe, &resp); err != nil {
		err = errors.Join(err, s.stopLocked())
		res.Problems = append(res.Problems, source.Problem{Code: help.GL1014, Args: []any{"detail", err.Error()}})
		return res, nil
	}
	if resp.Error != nil {
		res.Problems = append(res.Problems, source.Problem{Code: help.GL1014, Args: []any{"detail", resp.Error.Error()}})
		return res, nil
	}
	if resp.Version != nvmlproto.Version {
		res.Problems = append(res.Problems, source.Problem{Code: help.GL1014, Args: []any{"detail", fmt.Sprintf("%s speaks protocol %d, this binary %d", HelperName, resp.Version, nvmlproto.Version)}})
		return res, nil
	}
	s.driver = resp.Driver
	s.devices = map[int]source.Channel{}
	for _, d := range resp.Devices {
		if !d.Energy && !d.Power {
			continue
		}
		ch := source.Channel{
			ID:       ID + ":" + d.PCI,
			Source:   ID,
			Domain:   source.DomainGPU,
			Instance: d.PCI,
			Quality:  source.QualityMeasured,
			Method:   source.MethodMeasured,
			Kind:     source.KindCounter,
			Label:    d.Name,
		}
		if !d.Energy {
			ch.Method, ch.Kind = source.MethodIntegrated, source.KindPower
		}
		s.devices[d.Index] = ch
		res.Channels = append(res.Channels, ch)
	}
	return res, nil
}

// Read implements source.Source.
func (s *Source) Read(ctx context.Context) ([]source.Reading, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proc == nil || len(s.devices) == 0 {
		return nil, nil
	}
	var resp nvmlproto.ReadResponse
	if err := s.proc.exchange(ctx, nvmlproto.OpRead, &resp); err != nil {
		// The helper is gone or stuck; the channels have no value until
		// the source is probed again.
		return nil, errors.Join(err, s.stopLocked())
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	out := make([]source.Reading, 0, len(resp.Samples))
	usage := map[string]Usage{}
	var errs []error
	for _, smp := range resp.Samples {
		ch, ok := s.devices[smp.Index]
		if !ok {
			continue
		}
		if smp.Error != nil {
			errs = append(errs, fmt.Errorf("%s: %w", ch.ID, smp.Error))
			continue
		}
		r := source.Reading{Channel: ch.ID}
		if ch.Kind == source.KindCounter {
			r.EnergyJ = float64(smp.EnergyMJ) / 1e3
		} else {
			r.PowerW = float64(smp.PowerMW) / 1e3
		}
		out = append(out, r)
		usage[ch.Instance] = Usage{Util: smp.Util, PIDs: smp.PIDs, Processes: smp.Processes}
	}
	s.usage = usage
	return out, errors.Join(errs...)
}

// Close implements source.Source.
func (s *Source) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopLocked()
}

func (s *Source) stopLocked() error {
	if s.proc == nil {
		return nil
	}
	err := s.proc.stop()
	s.proc = nil
	return err
}

func (s *Source) candidates() []string {
	if s.helper != "" {
		return []string{s.helper}
	}
	var out []string
	if exe, err := os.Executable(); err == nil {
		out = append(out, filepath.Join(filepath.Dir(exe), HelperName))
	}
	for _, d := range SystemHelperDirs {
		out = append(out, filepath.Join(d, HelperName))
	}
	return out
}

func (s *Source) searched() string {
	var dirs []string
	for _, c := range s.candidates() {
		dirs = append(dirs, filepath.Dir(c))
	}
	return fmt.Sprint(dirs)
}

func (s *Source) findHelper() (string, error) {
	for _, c := range s.candidates() {
		if st, err := os.Stat(c); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			return c, nil
		}
	}
	return "", fs.ErrNotExist
}

// NVIDIADevices returns the PCI addresses of the display controllers bound
// to the NVIDIA driver. Cards on nouveau or without a driver have no NVML.
func NVIDIADevices(fsys fs.FS) ([]string, error) {
	const dir = "sys/bus/pci/devices"
	entries, err := fs.ReadDir(fsys, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		b, err := fs.ReadFile(fsys, path.Join(dir, e.Name(), "uevent"))
		if err != nil {
			continue
		}
		var driver, class string
		for line := range strings.Lines(string(b)) {
			k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
			switch k {
			case "DRIVER":
				driver = v
			case "PCI_CLASS":
				class = v
			}
		}
		// Class 0x03xxxx is a display controller; PCI_CLASS omits the
		// leading zeros.
		if driver == "nvidia" && len(class) == 5 && class[0] == '3' {
			out = append(out, strings.ToLower(e.Name()))
		}
	}
	return out, nil
}

// helperProc is a running greenlens-nvml.
type helperProc struct {
	cmd   *exec.Cmd
	in    io.WriteCloser
	lines chan []byte
	// finished is closed when the output of the helper ends; readErr says
	// why and is set before.
	finished chan struct{}
	readErr  error
}

func startHelper(path string) (*helperProc, error) {
	// The helper gets only the variables the dynamic loader needs.
	cmd := exec.Command(path) //nolint:gosec,noctx // the path comes from the configuration or a fixed directory; the helper lives as long as the source
	cmd.Env = []string{}
	if v, ok := os.LookupEnv("LD_LIBRARY_PATH"); ok {
		cmd.Env = append(cmd.Env, "LD_LIBRARY_PATH="+v)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", path, err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", path, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", path, err)
	}
	p := &helperProc{cmd: cmd, in: in, lines: make(chan []byte), finished: make(chan struct{})}
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 0, 4096), maxResponse)
		for sc.Scan() {
			p.lines <- append([]byte(nil), sc.Bytes()...)
		}
		p.readErr = sc.Err()
		if p.readErr == nil {
			p.readErr = io.ErrUnexpectedEOF
		}
		close(p.lines)
		close(p.finished)
	}()
	return p, nil
}

func (p *helperProc) exchange(ctx context.Context, op string, resp any) error {
	b, err := json.Marshal(nvmlproto.Request{Op: op})
	if err != nil {
		return err
	}
	if _, err := p.in.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("send %s to %s: %w", op, HelperName, err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case line, ok := <-p.lines:
		if !ok {
			return fmt.Errorf("%s exited: %w", HelperName, p.readErr)
		}
		if err := json.Unmarshal(line, resp); err != nil {
			return fmt.Errorf("decode %s answer of %s: %w", op, HelperName, err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%s did not answer %s: %w", HelperName, op, ctx.Err())
	}
}

// stop closes the input of the helper, which makes it shut NVML down and
// exit, and kills it if it does not.
func (p *helperProc) stop() error {
	_ = p.in.Close()
	// A line nobody waits for would block the reader.
	go func() {
		for range p.lines {
		}
	}()
	killed := false
	select {
	case <-p.finished:
	case <-time.After(timeout):
		_ = p.cmd.Process.Kill()
		killed = true
		<-p.finished
	}
	err := p.cmd.Wait()
	if killed {
		return fmt.Errorf("%s did not exit and was killed", HelperName)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", HelperName, err)
	}
	return nil
}
