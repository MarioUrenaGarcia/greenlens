// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Palette of GreenLens. Amber always means consumption and green
// efficiency; neither is used for decoration.
var (
	colorInk      = lipgloss.Color("#e8f7ef")
	colorMuted    = lipgloss.Color("#9cc4af")
	colorLoad     = lipgloss.Color("#f2a541")
	colorEff      = lipgloss.Color("#34d399")
	colorCritical = lipgloss.Color("#ef5a5a")
	colorSelected = lipgloss.Color("#16382a")
)

// domainColors are shades of amber, since every domain is consumption.
var domainColors = map[string]color.Color{
	"package":  lipgloss.Color("#f2a541"),
	"dram":     lipgloss.Color("#f7cf7d"),
	"gpu":      lipgloss.Color("#e07b39"),
	"psys":     lipgloss.Color("#c9a26b"),
	"platform": lipgloss.Color("#c9a26b"),
	"wall":     lipgloss.Color("#c9a26b"),
}

// theme holds the styles and glyphs of one way of drawing: with or without
// color, with or without Unicode.
type theme struct {
	color   bool
	unicode bool

	title, tab, tabOn, muted, value, load, eff, critical, keyHint, selected lipgloss.Style
	border                                                                  lipgloss.Border
}

func newTheme(useColor, useUnicode bool) theme {
	t := theme{color: useColor, unicode: useUnicode}
	s := lipgloss.NewStyle
	t.title = s().Bold(true)
	t.tab = s()
	t.tabOn = s().Bold(true).Reverse(true)
	t.muted = s()
	t.value = s().Bold(true)
	t.load = s().Bold(true)
	t.eff = s().Bold(true)
	t.critical = s().Bold(true)
	t.keyHint = s().Bold(true)
	t.selected = s().Reverse(true)
	if useColor {
		t.title = t.title.Foreground(colorInk)
		t.tab = t.tab.Foreground(colorMuted)
		t.tabOn = s().Bold(true).Foreground(colorInk).Background(colorSelected)
		t.muted = t.muted.Foreground(colorMuted)
		t.value = t.value.Foreground(colorInk)
		t.load = t.load.Foreground(colorLoad)
		t.eff = t.eff.Foreground(colorEff)
		t.critical = t.critical.Foreground(colorCritical)
		t.keyHint = t.keyHint.Foreground(colorInk)
		t.selected = s().Foreground(colorInk).Background(colorSelected)
	}
	t.border = lipgloss.ASCIIBorder()
	if useUnicode {
		t.border = lipgloss.NormalBorder()
	}
	return t
}

// domain styles a domain name or its line in the chart.
func (t theme) domain(d string) lipgloss.Style {
	st := lipgloss.NewStyle()
	if !t.color {
		return st
	}
	if c, ok := domainColors[d]; ok {
		return st.Foreground(c)
	}
	return st.Foreground(colorMuted)
}
