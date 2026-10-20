// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package selector chooses, for every domain, the channels of a single
// source, so the same energy is never counted twice: RAPL through powercap
// and through perf_event read the same counters, and a platform meter
// already includes the processor and the memory.
package selector

import (
	"slices"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/estimate"
)

// Candidate is a source with the result of its probe.
type Candidate struct {
	Source source.Source
	Result source.ProbeResult
}

// Options adjusts the automatic choice.
type Options struct {
	// Disable lists source IDs that must not be used.
	Disable []string
	// Force selects a source for a domain when it has channels for it.
	Force map[source.Domain]string
}

// Reason explains a decision with a catalog key under selector.reason and
// the name/value pairs of its placeholders.
type Reason struct {
	Key  string
	Args []any
}

// Decision records whether a channel is used and why.
type Decision struct {
	Channel  source.Channel `json:"channel"`
	Selected bool           `json:"selected"`
	Reason   Reason         `json:"-"`
}

// Selection is the outcome of Select.
type Selection struct {
	// Channels are the selected channels, ordered by source priority.
	Channels []source.Channel
	// Decisions covers every probed channel, selected or not.
	Decisions []Decision
	// Problems gathers the probe problems and those found while selecting.
	Problems []source.Problem
	// Estimated is true when the processor energy comes from the model.
	Estimated bool
}

// Selected reports whether a channel ID is part of the selection.
func (s Selection) Selected(id string) bool {
	return slices.ContainsFunc(s.Channels, func(c source.Channel) bool { return c.ID == id })
}

// Sources returns the IDs of the sources that have selected channels.
func (s Selection) Sources() []string {
	var out []string
	for _, c := range s.Channels {
		if !slices.Contains(out, c.Source) {
			out = append(out, c.Source)
		}
	}
	return out
}

// slot is what a single source is chosen for: a domain, or one GPU.
type slot struct {
	domain   source.Domain
	instance string
}

// slotOf returns the slot of a channel, and false for contained domains,
// whose slot is that of the package.
func slotOf(ch source.Channel) (slot, bool) {
	switch {
	case ch.Domain.Level() == source.LevelContained:
		return slot{domain: source.DomainPackage}, false
	case ch.Domain == source.DomainGPU:
		return slot{domain: ch.Domain, instance: ch.Instance}, true
	default:
		return slot{domain: ch.Domain}, true
	}
}

// Select applies the rules to the probed candidates. Candidates with the
// same priority keep the order in which they are given.
func Select(cands []Candidate, opt Options) Selection {
	var sel Selection
	ordered := slices.Clone(cands)
	slices.SortStableFunc(ordered, func(a, b Candidate) int { return a.Source.Priority() - b.Source.Priority() })

	for _, c := range ordered {
		sel.Problems = append(sel.Problems, c.Result.Problems...)
	}

	// winner holds the chosen source of each domain that has a value of its
	// own. GPUs are chosen one device at a time, because NVML, hwmon and the
	// model cover different cards. Contained domains follow the source of
	// the package.
	winner := map[slot]string{}
	var slots []slot
	for _, c := range ordered {
		for _, ch := range c.Result.Channels {
			if k, own := slotOf(ch); own && !slices.Contains(slots, k) {
				slots = append(slots, k)
			}
		}
	}
	usable := func(c Candidate) bool { return !slices.Contains(opt.Disable, c.Source.ID()) }
	has := func(c Candidate, k slot) bool {
		return slices.ContainsFunc(c.Result.Channels, func(ch source.Channel) bool {
			got, own := slotOf(ch)
			return own && got == k
		})
	}
	for _, k := range slots {
		if forced, ok := opt.Force[k.domain]; ok {
			for _, c := range ordered {
				if c.Source.ID() == forced && usable(c) && has(c, k) {
					winner[k] = forced
				}
			}
			if _, ok := winner[k]; ok {
				continue
			}
		}
		for _, c := range ordered {
			if usable(c) && has(c, k) {
				winner[k] = c.Source.ID()
				break
			}
		}
	}

	for _, c := range ordered {
		for _, ch := range c.Result.Channels {
			dec := Decision{Channel: ch}
			k, _ := slotOf(ch)
			owner := winner[k]
			switch {
			case !usable(c):
				dec.Reason = Reason{Key: "selector.reason.disabled"}
			case owner == ch.Source:
				dec.Selected = true
				dec.Reason = Reason{Key: "selector.reason.selected"}
				sel.Channels = append(sel.Channels, ch)
			case ch.Domain.Level() == source.LevelContained:
				dec.Reason = Reason{Key: "selector.reason.contained", Args: []any{"source", owner}}
			case opt.Force[ch.Domain] == owner && owner != "":
				dec.Reason = Reason{Key: "selector.reason.forced", Args: []any{"source", owner}}
			default:
				dec.Reason = Reason{Key: "selector.reason.duplicate", Args: []any{"source", owner}}
			}
			sel.Decisions = append(sel.Decisions, dec)
		}
	}

	if winner[slot{domain: source.DomainPackage}] == estimate.ID {
		sel.Estimated = true
		sel.Problems = append(sel.Problems, source.Problem{Code: help.GL1007, Channel: estimate.ChannelID})
	}
	return sel
}
