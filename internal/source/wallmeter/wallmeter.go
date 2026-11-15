// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package wallmeter reads the power a machine draws at the wall from a
// smart plug on the local network that measures it: a Shelly, of the first
// generation or a later one, or a device with the Tasmota firmware. Such a
// meter sees everything behind the plug, power supply losses included,
// which makes it the reference to validate the other sources against.
package wallmeter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/intranet"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/poller"
)

// ID names this source.
const ID = "wall_meter"

// Kinds of meter.
const (
	KindShelly  = "shelly"
	KindTasmota = "tasmota"
)

// Kinds lists every kind.
var Kinds = []string{KindShelly, KindTasmota}

// MaxChannel is the highest output a meter can be asked for.
const MaxChannel = 15

const (
	// maxBody bounds an answer; a status is under a kilobyte.
	maxBody = 64 << 10
	// probeTimeout bounds the two requests of the probe.
	probeTimeout = 5 * time.Second
)

// Options configures the source.
type Options struct {
	Enabled bool
	// Kind is the family of the meter.
	Kind string
	// Endpoint is the http URL of the meter.
	Endpoint string
	// Channel is the output of a meter with several, counted from zero.
	Channel int
	// Interval is the time between two readings.
	Interval time.Duration
}

// CheckEndpoint checks the address of a meter: http, with a host on the
// internal network and no path.
func CheckEndpoint(raw string) error {
	u, err := intranet.CheckURL(raw, "http")
	if err != nil {
		return err
	}
	if u.Path != "" && u.Path != "/" {
		return errors.New("the URL is the address of the meter, without a path")
	}
	return nil
}

// Source reads the power of one output of a meter.
type Source struct {
	o      Options
	client *http.Client
	base   *url.URL
	// read asks the meter for its power in the way of its kind.
	read poller.Fetch
	ch   source.Channel
	poll *poller.Poller
}

// New returns the source. It makes no request until it is probed.
func New(o Options) *Source { return &Source{o: o} }

// ID implements source.Source.
func (s *Source) ID() string { return ID }

// Priority implements source.Source.
func (s *Source) Priority() int { return 5 }

// Close implements source.Source.
func (s *Source) Close() error {
	if s.poll != nil {
		s.poll.Stop()
		s.poll = nil
	}
	if s.client != nil {
		s.client.CloseIdleConnections()
	}
	return nil
}

func problem(detail string) []source.Problem {
	return []source.Problem{{Code: help.GL1017, Args: []any{"detail", detail}}}
}

// Probe implements source.Source. It finds how the meter is asked for its
// power, reads it once and starts reading it in the background.
func (s *Source) Probe(ctx context.Context) (source.ProbeResult, error) {
	res := source.ProbeResult{Source: ID}
	if err := s.Close(); err != nil {
		return res, err
	}
	if !s.o.Enabled {
		return res, nil
	}
	base, err := intranet.CheckURL(s.o.Endpoint, "http")
	if err != nil {
		res.Problems = problem(err.Error())
		return res, nil
	}
	if s.o.Channel < 0 || s.o.Channel > MaxChannel {
		res.Problems = problem(fmt.Sprintf("channel %d is outside 0 to %d", s.o.Channel, MaxChannel))
		return res, nil
	}
	s.base = base
	s.client = &http.Client{Transport: intranet.Transport(nil), CheckRedirect: intranet.NoRedirect}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	label := ""
	switch s.o.Kind {
	case KindShelly:
		label, s.read, err = s.shelly(ctx)
	case KindTasmota:
		label, s.read = "Tasmota", s.tasmota
	default:
		err = fmt.Errorf("unknown kind %q", s.o.Kind)
	}
	var watts float64
	if err == nil {
		watts, err = s.read(ctx)
	}
	if err != nil {
		res.Problems = problem(err.Error())
		return res, nil
	}
	instance := strconv.Itoa(s.o.Channel)
	s.ch = source.Channel{
		ID:       ID + ":" + instance,
		Source:   ID,
		Domain:   source.DomainWall,
		Instance: instance,
		Quality:  source.QualityPlatform,
		Method:   source.MethodIntegrated,
		Kind:     source.KindPower,
		Label:    label + " output " + instance,
	}
	s.poll = poller.New(s.o.Interval, s.read)
	s.poll.Set(watts)
	s.poll.Start() //nolint:contextcheck // the readings go on after the probe returns
	res.Channels = append(res.Channels, s.ch)
	return res, nil
}

// Read implements source.Source. It returns the latest value read in the
// background, and nothing while the meter does not answer.
func (s *Source) Read(context.Context) ([]source.Reading, error) {
	if s.poll == nil {
		return nil, nil
	}
	w, ok, err := s.poll.Latest()
	if !ok {
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.ch.ID, err)
		}
		return nil, nil
	}
	return []source.Reading{{Channel: s.ch.ID, PowerW: w}}, nil
}

// shelly identifies the generation of the device with /shelly, which
// every Shelly answers without a password. Devices of the second
// generation onward state it in "gen"; those of the first do not.
func (s *Source) shelly(ctx context.Context) (string, poller.Fetch, error) {
	var info struct {
		Gen  int  `json:"gen"`
		Auth bool `json:"auth"`
		// AuthEn is how devices of the second generation onward say that
		// they ask for a password.
		AuthEn bool `json:"auth_en"`
	}
	if err := s.get(ctx, "/shelly", "", &info); err != nil {
		return "", nil, err
	}
	if info.Auth || info.AuthEn {
		return "", nil, errors.New("the meter asks for a password, which is not supported")
	}
	if info.Gen >= 2 {
		return "Shelly", s.shellyRPC, nil
	}
	return "Shelly", s.shellyMeter, nil
}

// shellyRPC reads a device of the second generation onward:
// Switch.GetStatus reports the active power in watts as "apower".
func (s *Source) shellyRPC(ctx context.Context) (float64, error) {
	var st struct {
		APower *float64 `json:"apower"`
	}
	if err := s.get(ctx, "/rpc/Switch.GetStatus", "id="+strconv.Itoa(s.o.Channel), &st); err != nil {
		return 0, err
	}
	if st.APower == nil {
		return 0, errors.New("the switch reports no power; this model may not measure it")
	}
	return valid(*st.APower)
}

// shellyMeter reads a device of the first generation: /meter/{index}
// reports the power in watts as "power" and whether the value is valid.
func (s *Source) shellyMeter(ctx context.Context) (float64, error) {
	var m struct {
		Power   *float64 `json:"power"`
		IsValid *bool    `json:"is_valid"`
	}
	if err := s.get(ctx, "/meter/"+strconv.Itoa(s.o.Channel), "", &m); err != nil {
		return 0, err
	}
	if m.Power == nil || (m.IsValid != nil && !*m.IsValid) {
		return 0, errors.New("the meter reports no valid power")
	}
	return valid(*m.Power)
}

// tasmota reads a device with the Tasmota firmware: "Status 10" reports
// the sensors, and a power monitoring device has its power in watts under
// StatusSNS.ENERGY.Power, as a number or as one number per output.
func (s *Source) tasmota(ctx context.Context) (float64, error) {
	var st struct {
		StatusSNS struct {
			Energy *struct {
				Power json.RawMessage `json:"Power"`
			} `json:"ENERGY"`
		} `json:"StatusSNS"`
		Warning string `json:"WARNING"`
	}
	if err := s.get(ctx, "/cm", "cmnd="+url.QueryEscape("Status 10"), &st); err != nil {
		return 0, err
	}
	if st.Warning != "" {
		return 0, errors.New("the meter asks for a password, which is not supported")
	}
	if st.StatusSNS.Energy == nil || len(st.StatusSNS.Energy.Power) == 0 {
		return 0, errors.New("the device reports no ENERGY sensor; it may not measure power")
	}
	var one float64
	if err := json.Unmarshal(st.StatusSNS.Energy.Power, &one); err == nil {
		if s.o.Channel != 0 {
			return 0, fmt.Errorf("the device has one output; channel %d does not exist", s.o.Channel)
		}
		return valid(one)
	}
	var many []float64
	if err := json.Unmarshal(st.StatusSNS.Energy.Power, &many); err != nil {
		return 0, errors.New("the power of the ENERGY sensor is not a number")
	}
	if s.o.Channel >= len(many) {
		return 0, fmt.Errorf("the device has %d outputs; channel %d does not exist", len(many), s.o.Channel)
	}
	return valid(many[s.o.Channel])
}

func valid(w float64) (float64, error) {
	if w < 0 || math.IsNaN(w) || math.IsInf(w, 0) {
		return 0, fmt.Errorf("the meter reports %v W", w)
	}
	return w, nil
}

// get reads a JSON document of the meter.
func (s *Source) get(ctx context.Context, path, query string, v any) error {
	u := *s.base
	u.Path, u.RawQuery = path, query
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return err
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return errors.New("the meter asks for a password, which is not supported")
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%s answered %s", path, resp.Status)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("%s did not answer with JSON; check sources.wall_meter.kind", path)
	}
	return nil
}
