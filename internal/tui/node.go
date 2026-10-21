// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

// chartOrder lists the domains in the order they are drawn and listed.
var chartOrder = []string{"package", "dram", "gpu", "psys", "platform", "wall"}

func apiDomain(d string) source.Domain { return source.Domain(d) }

// domainOrder returns the domains the agent reports that have a line of
// their own; cores and uncore are part of the package.
func (m *model) domainOrder() []string {
	have := map[string]bool{}
	for _, d := range m.status.Domains {
		if d.Level != "contained" {
			have[string(d.Domain)] = true
		}
	}
	var out []string
	for _, d := range chartOrder {
		if have[d] {
			out = append(out, d)
		}
	}
	return out
}

// marks tell the lines apart where color cannot.
var marks = []string{"*", "+", "#", "o", "x", "="}

func (m *model) nodeView() string {
	domains := m.domainOrder()
	if len(domains) == 0 {
		return m.t.muted.Render(m.l.T("tui.empty.node"))
	}
	byDomain := map[string]int{}
	for i, d := range domains {
		byDomain[d] = i
	}
	lines := make([]series, len(domains))
	for i, d := range domains {
		lines[i].name = d
	}
	peak := 0.0
	for _, p := range m.live.Points {
		for i, d := range domains {
			w := p.PowerW[apiDomain(d)]
			lines[i].values = append(lines[i].values, w)
			peak = max(peak, w)
		}
	}
	top := niceTop(peak)

	const axis = 8
	legendRows := len(domains) + 2
	height := m.height - 4 - legendRows
	width := m.width - axis
	// Braille needs color to tell lines apart; without it every line gets
	// its own mark.
	ct := m.t
	if !m.t.color {
		ct.unicode = false
	}
	grid := chartWithMarks(ct, lines, width, height, top)
	var b strings.Builder
	for i, row := range grid {
		label := ""
		switch i {
		case 0:
			label = fmtPower(top, m.o.Raw)
		case len(grid) - 1:
			label = fmtPower(0, m.o.Raw)
		case len(grid) / 2:
			label = fmtPower(top/2, m.o.Raw)
		}
		b.WriteString(m.t.muted.Render(fmt.Sprintf("%*s ", axis-1, label)))
		b.WriteString(row + "\n")
	}
	minutes := 0.0
	if n := len(m.live.Points); n > 1 {
		minutes = m.live.Points[n-1].Time.Sub(m.live.Points[0].Time).Minutes()
	}
	b.WriteString(m.t.muted.Render(fmt.Sprintf("%*s%s", axis, "", m.l.T("tui.chart_window", "minutes", fmt.Sprintf("%.0f", minutes)))) + "\n")

	b.WriteString(m.t.muted.Render(fmt.Sprintf("%-22s %10s %12s  %s", m.l.T("tui.col.domain"), m.l.T("tui.col.power"), m.l.T("tui.col.energy_start"), m.l.T("tui.col.quality"))) + "\n")
	for _, d := range m.status.Domains {
		i, ok := byDomain[string(d.Domain)]
		if !ok {
			continue
		}
		name := m.l.T("domain." + string(d.Domain))
		if !m.t.color {
			name = marks[i%len(marks)] + " " + name
		}
		name = truncate(name, 22)
		pad := strings.Repeat(" ", max(0, 22-len([]rune(name))))
		b.WriteString(m.t.domain(string(d.Domain)).Render(name) + pad + " ")
		b.WriteString(m.t.load.Render(fmt.Sprintf("%10s", fmtPower(d.PowerW, m.o.Raw))) + " ")
		b.WriteString(m.t.value.Render(fmt.Sprintf("%12s", fmtEnergy(d.EnergyJ, m.o.Raw))) + "  ")
		b.WriteString(m.t.muted.Render(m.l.T("quality."+string(d.Quality))) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// chartWithMarks draws like chart, but in ASCII every series uses its own
// mark.
func chartWithMarks(t theme, lines []series, width, height int, top float64) []string {
	if t.unicode {
		return chart(t, lines, width, height, top)
	}
	out := make([][]string, height)
	for i := range out {
		out[i] = slices.Repeat([]string{" "}, width)
	}
	for si, s := range lines {
		mark := t.domain(s.name).Render(marks[si%len(marks)])
		for i, row := range chart(theme{}, []series{s}, width, height, top) {
			for j, ch := range []rune(row) {
				if ch != ' ' {
					out[i][j] = mark
				}
			}
		}
	}
	rows := make([]string, height)
	for i, r := range out {
		rows[i] = strings.Join(r, "")
	}
	return rows
}
