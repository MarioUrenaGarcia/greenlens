// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package ipmi reads the power of the whole server from its baseboard
// management controller with the DCMI "Get Power Reading" command, through
// the Linux IPMI device driver.
package ipmi

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

// ID names this source.
const ID = "ipmi"

// Device nodes created by the IPMI device interface, depending on the udev
// rules of the distribution.
var devicePaths = []string{"dev/ipmi0", "dev/ipmi/0", "dev/ipmidev/0"}

// DCMI Get Power Reading (DCMI 1.5, group extension 0xDC). The request is
// the group id, the mode (1: system power statistics) and two reserved
// bytes.
const (
	netfnDCMI          = 0x2C
	cmdGetPowerReading = 0x02
	dcmiGroupID        = 0xDC
	modeSystemPower    = 0x01
	responseLen        = 19
	stateActive        = 0x40
)

// Device sends one request to the BMC. The response data starts with the
// completion code, as the Linux driver returns it.
type Device interface {
	Request(netfn, cmd byte, data []byte) ([]byte, error)
	Close() error
}

// Opener opens the IPMI device node at an absolute path.
type Opener interface {
	Open(path string) (Device, error)
}

// Source reads the server power from the BMC.
type Source struct {
	root   string
	opener Opener
	dev    Device
	ch     source.Channel
}

// New returns a source that looks for the device under the host root.
func New(root string, opener Opener) *Source { return &Source{root: root, opener: opener} }

// ID implements source.Source.
func (s *Source) ID() string { return ID }

// Priority implements source.Source.
func (s *Source) Priority() int { return 4 }

// Close implements source.Source.
func (s *Source) Close() error {
	if s.dev == nil {
		return nil
	}
	err := s.dev.Close()
	s.dev = nil
	return err
}

// Probe implements source.Source.
func (s *Source) Probe(_ context.Context) (source.ProbeResult, error) {
	res := source.ProbeResult{Source: ID}
	if err := s.Close(); err != nil {
		return res, err
	}
	for _, p := range devicePaths {
		full := filepath.Join(s.root, p)
		dev, err := s.opener.Open(full)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				res.Problems = append(res.Problems, source.Problem{Code: help.GL1004, Args: []any{"path", "/" + p}})
				return res, nil
			}
			return res, fmt.Errorf("open %s: %w", full, err)
		}
		_, active, err := readPower(dev)
		if err != nil || !active {
			detail := "an inactive power measurement"
			if err != nil {
				detail = err.Error()
			}
			res.Problems = append(res.Problems, source.Problem{Code: help.GL1006, Args: []any{"detail", detail}})
			return res, dev.Close()
		}
		s.dev = dev
		s.ch = source.Channel{
			ID:       ID + ":dcmi",
			Source:   ID,
			Domain:   source.DomainPlatform,
			Instance: "bmc",
			Quality:  source.QualityPlatform,
			Method:   source.MethodIntegrated,
			Kind:     source.KindPower,
			Label:    "DCMI power reading",
		}
		res.Channels = append(res.Channels, s.ch)
		return res, nil
	}
	return res, nil
}

// Read implements source.Source.
func (s *Source) Read(_ context.Context) ([]source.Reading, error) {
	if s.dev == nil {
		return nil, nil
	}
	w, active, err := readPower(s.dev)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.ch.ID, err)
	}
	if !active {
		return nil, nil
	}
	return []source.Reading{{Channel: s.ch.ID, PowerW: w}}, nil
}

func readPower(dev Device) (float64, bool, error) {
	resp, err := dev.Request(netfnDCMI, cmdGetPowerReading, []byte{dcmiGroupID, modeSystemPower, 0x00, 0x00})
	if err != nil {
		return 0, false, err
	}
	return parsePowerReading(resp)
}

// parsePowerReading decodes the response: completion code, group id,
// current, minimum, maximum and average power (16-bit little endian watts),
// timestamp, reporting period and the reading state, whose bit 6 tells
// whether power measurement is active.
func parsePowerReading(resp []byte) (float64, bool, error) {
	if len(resp) == 0 {
		return 0, false, errors.New("empty response")
	}
	if resp[0] != 0 {
		return 0, false, fmt.Errorf("completion code 0x%02x", resp[0])
	}
	if len(resp) < responseLen {
		return 0, false, fmt.Errorf("short response of %d bytes", len(resp))
	}
	if resp[1] != dcmiGroupID {
		return 0, false, fmt.Errorf("unexpected group id 0x%02x", resp[1])
	}
	current := binary.LittleEndian.Uint16(resp[2:4])
	return float64(current), resp[18]&stateActive != 0, nil
}
