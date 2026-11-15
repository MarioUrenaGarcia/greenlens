// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package redfish reads the power of the whole server from the Redfish
// service of its baseboard management controller, over the management
// network. It reads the EnvironmentMetrics resource of a chassis, and the
// Power resource it replaced on controllers that predate it.
package redfish

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/intranet"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/poller"
)

// ID names this source.
const ID = "redfish"

const (
	// chassisCollection is the collection every Redfish service has.
	chassisCollection = "/redfish/v1/Chassis"
	// sessionHeader carries the token of a Redfish session.
	sessionHeader = "X-Auth-Token"
	// maxBody bounds a resource; a chassis is a few kilobytes.
	maxBody = 1 << 20
	// probeTimeout bounds the discovery, which makes a few requests.
	probeTimeout = 10 * time.Second
	// maxChassis bounds how many chassis are looked at for a reading.
	maxChassis = 16
)

// Options configures the source.
type Options struct {
	Enabled bool
	// Endpoint is the https URL of the controller.
	Endpoint string
	// Token is the token of a Redfish session.
	Token []byte
	// Roots are the authorities that sign the certificate of the
	// controller; nil uses those of the system.
	Roots *x509.CertPool
	// Chassis picks one chassis by the last part of its path; empty uses
	// the first one that reports power.
	Chassis string
	// Interval is the time between two readings.
	Interval time.Duration
	// Err is why the options could not be built, such as a token file
	// that cannot be read; Probe reports it.
	Err error
}

// CheckEndpoint checks the address of a controller: https, with a host on
// the internal network. The token of the session travels in every request,
// so plain http is not accepted.
func CheckEndpoint(raw string) error {
	u, err := intranet.CheckURL(raw, "https")
	if err != nil {
		return err
	}
	if u.Path != "" && u.Path != "/" {
		return errors.New("the URL is the address of the controller, without a path")
	}
	return nil
}

// Source reads the power of a chassis.
type Source struct {
	o      Options
	client *http.Client
	base   *url.URL
	// reading is the resource that holds the power, and legacy whether it
	// is a Power resource.
	reading string
	legacy  bool
	ch      source.Channel
	poll    *poller.Poller
}

// New returns the source. It makes no request until it is probed.
func New(o Options) *Source { return &Source{o: o} }

// ID implements source.Source.
func (s *Source) ID() string { return ID }

// Priority implements source.Source.
func (s *Source) Priority() int { return 4 }

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
	return []source.Problem{{Code: help.GL1016, Args: []any{"detail", detail}}}
}

// Probe implements source.Source. It finds the chassis and the resource
// that reports its power, and starts reading it in the background.
func (s *Source) Probe(ctx context.Context) (source.ProbeResult, error) {
	res := source.ProbeResult{Source: ID}
	if err := s.Close(); err != nil {
		return res, err
	}
	if !s.o.Enabled {
		return res, nil
	}
	if s.o.Err != nil {
		res.Problems = problem(s.o.Err.Error())
		return res, nil
	}
	base, err := intranet.CheckURL(s.o.Endpoint, "https")
	if err != nil {
		res.Problems = problem(err.Error())
		return res, nil
	}
	s.base = base
	s.client = &http.Client{Transport: intranet.Transport(s.o.Roots), CheckRedirect: intranet.NoRedirect}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	id, watts, err := s.discover(ctx)
	if err != nil {
		res.Problems = problem(err.Error())
		return res, nil
	}
	s.ch = source.Channel{
		ID:       ID + ":" + id,
		Source:   ID,
		Domain:   source.DomainPlatform,
		Instance: id,
		Quality:  source.QualityPlatform,
		Method:   source.MethodIntegrated,
		Kind:     source.KindPower,
		Label:    "Redfish chassis " + id,
	}
	s.poll = poller.New(s.o.Interval, s.fetch)
	s.poll.Set(watts)
	s.poll.Start() //nolint:contextcheck // the readings go on after the probe returns
	res.Channels = append(res.Channels, s.ch)
	return res, nil
}

// Read implements source.Source. It returns the latest value read in the
// background, and nothing while the controller does not answer.
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

type link struct {
	ID string `json:"@odata.id"`
}

type collection struct {
	Members []link `json:"Members"`
}

type chassis struct {
	EnvironmentMetrics link `json:"EnvironmentMetrics"`
	Power              link `json:"Power"`
}

// environmentMetrics is the part of EnvironmentMetrics that holds the
// power of the chassis, a sensor excerpt.
type environmentMetrics struct {
	PowerWatts *struct {
		Reading *float64 `json:"Reading"`
	} `json:"PowerWatts"`
}

// power is the part of the Power resource, deprecated since Redfish
// 2020.4, that holds the power the chassis consumes.
type power struct {
	PowerControl []struct {
		PowerConsumedWatts *float64 `json:"PowerConsumedWatts"`
	} `json:"PowerControl"`
}

// discover returns the chassis to read, with its first reading.
func (s *Source) discover(ctx context.Context) (string, float64, error) {
	var members []string
	if s.o.Chassis != "" {
		members = []string{path.Join(chassisCollection, s.o.Chassis)}
	} else {
		var col collection
		if err := s.get(ctx, chassisCollection, &col); err != nil {
			return "", 0, err
		}
		for _, m := range col.Members {
			members = append(members, m.ID)
		}
		if len(members) == 0 {
			return "", 0, errors.New("the service lists no chassis")
		}
	}
	if len(members) > maxChassis {
		members = members[:maxChassis]
	}
	var last error
	for _, m := range members {
		var c chassis
		if err := s.get(ctx, m, &c); err != nil {
			last = err
			continue
		}
		for _, candidate := range []struct {
			path   string
			legacy bool
		}{{c.EnvironmentMetrics.ID, false}, {c.Power.ID, true}} {
			if candidate.path == "" {
				continue
			}
			s.reading, s.legacy = candidate.path, candidate.legacy
			w, err := s.fetch(ctx)
			if err != nil {
				last = err
				continue
			}
			return path.Base(m), w, nil
		}
	}
	s.reading = ""
	if last != nil {
		return "", 0, last
	}
	return "", 0, errors.New("no chassis reports its power")
}

// fetch reads the power of the chosen resource.
func (s *Source) fetch(ctx context.Context) (float64, error) {
	if s.legacy {
		var p power
		if err := s.get(ctx, s.reading, &p); err != nil {
			return 0, err
		}
		for _, c := range p.PowerControl {
			if c.PowerConsumedWatts != nil {
				return valid(*c.PowerConsumedWatts)
			}
		}
		return 0, errors.New("the Power resource has no PowerConsumedWatts")
	}
	var m environmentMetrics
	if err := s.get(ctx, s.reading, &m); err != nil {
		return 0, err
	}
	if m.PowerWatts == nil || m.PowerWatts.Reading == nil {
		return 0, errors.New("the EnvironmentMetrics resource has no PowerWatts reading")
	}
	return valid(*m.PowerWatts.Reading)
}

func valid(w float64) (float64, error) {
	if w < 0 || math.IsNaN(w) || math.IsInf(w, 0) {
		return 0, fmt.Errorf("the controller reports %v W", w)
	}
	return w, nil
}

// get reads one resource of the service. Resources are named by absolute
// paths of the same service; anything else, such as a URL of another host,
// is refused.
func (s *Source) get(ctx context.Context, resource string, v any) error {
	if !strings.HasPrefix(resource, "/redfish/") || strings.Contains(resource, "..") {
		return fmt.Errorf("unexpected resource path %q", resource)
	}
	u := *s.base
	u.Path, u.RawQuery = resource, ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OData-Version", "4.0")
	if len(s.o.Token) > 0 {
		req.Header.Set(sessionHeader, string(s.o.Token))
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
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%s answered %s: the session token is missing, expired or lacks the Login privilege", resource, resp.Status)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%s answered %s", resource, resp.Status)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("%s is not a JSON resource: %w", resource, err)
	}
	return nil
}
