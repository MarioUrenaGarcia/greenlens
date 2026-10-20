// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package nvidiasmi reads the power of NVIDIA GPUs from nvidia-smi, for
// machines where greenlens-nvml is not installed. nvidia-smi runs in loop
// mode, printing one line per GPU every second, so no process is started
// per sample. Its power is the board power in watts, averaged over one
// second on Ampere and later and instantaneous before, accurate to within
// 5 W according to its documentation.
package nvidiasmi

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/nvmlproto"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/nvml"
)

// ID is the source ID.
const ID = "nvidia_smi"

// Command is the program looked up in PATH.
const Command = "nvidia-smi"

// query lists the fields read, as named by nvidia-smi --help-query-gpu.
// The name goes last because it is free text.
const query = "--query-gpu=pci.bus_id,power.draw,name"

// probeTimeout bounds the first run, which initializes the driver.
const probeTimeout = 10 * time.Second

// stale is how old the last line of a GPU may be before it is not used.
const stale = 5 * time.Second

type value struct {
	w  float64
	at time.Time
}

// Source reads NVIDIA GPUs through nvidia-smi.
type Source struct {
	fsys fs.FS
	path string
	now  func() time.Time

	mu       sync.Mutex
	channels map[string]source.Channel
	stream   *exec.Cmd
	exited   chan struct{}
	values   map[string]value
	err      error
}

// New returns the source. path is the nvidia-smi binary; when empty it is
// looked up in PATH.
func New(fsys fs.FS, path string) *Source {
	return &Source{fsys: fsys, path: path, now: time.Now}
}

// ID implements source.Source.
func (s *Source) ID() string { return ID }

// Priority implements source.Source; NVML is preferred.
func (s *Source) Priority() int { return 2 }

func (s *Source) args(loop bool) []string {
	a := []string{query, "--format=csv,noheader,nounits"}
	if loop {
		a = append(a, "--loop-ms=1000")
	}
	return a
}

// Probe implements source.Source.
func (s *Source) Probe(ctx context.Context) (source.ProbeResult, error) {
	res := source.ProbeResult{Source: ID}
	gpus, err := nvml.NVIDIADevices(s.fsys)
	if err != nil || len(gpus) == 0 {
		return res, err
	}
	path := s.path
	if path == "" {
		if path, err = exec.LookPath(Command); err != nil {
			// nvidia-smi is only a fallback; GL1013 already says what to
			// install when NVML is missing too.
			return res, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, s.args(false)...).Output() //nolint:gosec // the path is nvidia-smi from PATH or the configuration
	if err != nil {
		res.Problems = append(res.Problems, source.Problem{Code: help.GL1015, Args: []any{"detail", err.Error()}})
		return res, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.path = path
	s.channels = map[string]source.Channel{}
	for line := range strings.Lines(string(out)) {
		pci, _, name, ok := parse(line)
		if !ok {
			continue
		}
		ch := source.Channel{
			ID:       ID + ":" + pci,
			Source:   ID,
			Domain:   source.DomainGPU,
			Instance: pci,
			Quality:  source.QualityMeasured,
			Method:   source.MethodIntegrated,
			Kind:     source.KindPower,
			Label:    name,
		}
		s.channels[pci] = ch
		res.Channels = append(res.Channels, ch)
	}
	return res, nil
}

// parse reads one line such as "00000000:01:00.0, 17.03, NVIDIA GeForce
// RTX 3050". A GPU without power management prints "[N/A]" or "[Not
// Supported]" and is skipped.
func parse(line string) (pci string, w float64, name string, ok bool) {
	f := strings.SplitN(strings.TrimSpace(line), ",", 3)
	if len(f) != 3 {
		return "", 0, "", false
	}
	pci, err := nvmlproto.PCIAddress(strings.TrimSpace(f[0]))
	if err != nil {
		return "", 0, "", false
	}
	w, err = strconv.ParseFloat(strings.TrimSpace(f[1]), 64)
	if err != nil || w < 0 {
		return "", 0, "", false
	}
	return pci, w, strings.TrimSpace(f[2]), true
}

// Read implements source.Source. The first call starts nvidia-smi in loop
// mode; GPUs appear once it has printed their first line.
func (s *Source) Read(context.Context) ([]source.Reading, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.channels) == 0 {
		return nil, nil
	}
	if s.stream == nil {
		if err := s.start(); err != nil {
			return nil, err
		}
	}
	select {
	case <-s.exited:
		err := s.err
		s.stream = nil
		return nil, fmt.Errorf("%s exited: %w", Command, err)
	default:
	}
	now := s.now()
	out := make([]source.Reading, 0, len(s.channels))
	for pci, ch := range s.channels {
		v, ok := s.values[pci]
		if !ok || now.Sub(v.at) > stale {
			continue
		}
		out = append(out, source.Reading{Channel: ch.ID, PowerW: v.w})
	}
	return out, nil
}

func (s *Source) start() error {
	// The loop lives as long as the source, not as one request.
	cmd := exec.Command(s.path, s.args(true)...) //nolint:gosec,noctx // the path is nvidia-smi from PATH or the configuration
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("start %s: %w", Command, err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", Command, err)
	}
	s.stream, s.exited, s.values, s.err = cmd, make(chan struct{}), map[string]value{}, nil
	exited := s.exited
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			pci, w, _, ok := parse(sc.Text())
			if !ok {
				continue
			}
			s.mu.Lock()
			s.values[pci] = value{w: w, at: s.now()}
			s.mu.Unlock()
		}
		err := cmd.Wait()
		if err == nil {
			err = errors.New("end of output")
		}
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		close(exited)
	}()
	return nil
}

// Close implements source.Source.
func (s *Source) Close() error {
	s.mu.Lock()
	cmd, exited := s.stream, s.exited
	s.stream = nil
	s.mu.Unlock()
	if cmd == nil {
		return nil
	}
	_ = cmd.Process.Kill()
	<-exited
	return nil
}
