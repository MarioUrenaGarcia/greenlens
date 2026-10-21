// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package tui

import (
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/MarioUrenaGarcia/greenlens/internal/units"
)

func fmtPower(w float64, raw bool) string  { return units.Power(w, raw) }
func fmtEnergy(j float64, raw bool) string { return units.Energy(j, raw) }
func fmtCO2e(g float64, raw bool) string   { return units.CO2e(g, raw) }

func fmtCost(v *float64, currency string, raw bool) string {
	if v == nil {
		return "-"
	}
	return units.Money(*v, currency, raw)
}

func fmtRate(r float64) string {
	if r <= 0 {
		return "-"
	}
	return strconv.FormatFloat(r, 'f', 1, 64)
}

func num(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

func numPtr(v *float64) string {
	if v == nil {
		return ""
	}
	return num(*v)
}

func deref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

// asciiFold replaces the letters of Spanish and other Latin texts with
// their unaccented form, and any other character outside ASCII with "?",
// for terminals that cannot show Unicode.
func asciiFold(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x80:
			b.WriteRune(r)
		case strings.ContainsRune("áàäâã", r):
			b.WriteByte('a')
		case strings.ContainsRune("éèëê", r):
			b.WriteByte('e')
		case strings.ContainsRune("íìïî", r):
			b.WriteByte('i')
		case strings.ContainsRune("óòöôõ", r):
			b.WriteByte('o')
		case strings.ContainsRune("úùüû", r):
			b.WriteByte('u')
		case strings.ContainsRune("ÁÀÄÂÃ", r):
			b.WriteByte('A')
		case strings.ContainsRune("ÉÈËÊ", r):
			b.WriteByte('E')
		case strings.ContainsRune("ÍÌÏÎ", r):
			b.WriteByte('I')
		case strings.ContainsRune("ÓÒÖÔÕ", r):
			b.WriteByte('O')
		case strings.ContainsRune("ÚÙÜÛ", r):
			b.WriteByte('U')
		case r == 'ñ':
			b.WriteByte('n')
		case r == 'Ñ':
			b.WriteByte('N')
		case r == 'ç':
			b.WriteByte('c')
		case r == '¿' || r == '¡':
		default:
			b.WriteByte('?')
		}
	}
	return b.String()
}

// truncate shortens plain text to width cells with an ASCII ellipsis.
func truncate(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= 3 {
		return strings.Repeat(".", max(0, width))
	}
	r := []rune(s)
	for lipgloss.Width(string(r))+3 > width && len(r) > 0 {
		r = r[:len(r)-1]
	}
	return string(r) + "..."
}

// clip cuts a styled line to width cells, so no line wraps.
func clip(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}

// spread places left and right at the edges of width cells.
func spread(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// wrapText breaks prose at spaces to width cells.
func wrapText(s string, width int) string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, w := range strings.Fields(para) {
			if line != "" && lipgloss.Width(line)+1+lipgloss.Width(w) > width {
				out = append(out, line)
				line = w
				continue
			}
			if line != "" {
				line += " "
			}
			line += w
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// input is the text of the filter. It takes printable keys only, so
// nothing is read from the clipboard.
type input struct {
	runes []rune
}

func (in input) value() string { return string(in.runes) }

const maxFilter = 64

func (in *input) key(k tea.KeyPressMsg) {
	switch k.String() {
	case "backspace":
		if len(in.runes) > 0 {
			in.runes = in.runes[:len(in.runes)-1]
		}
	case "ctrl+u":
		in.runes = nil
	default:
		for _, r := range k.Text {
			if unicode.IsPrint(r) && len(in.runes) < maxFilter {
				in.runes = append(in.runes, r)
			}
		}
	}
}

func (in input) view(t theme) string {
	return t.value.Render(in.value()) + t.keyHint.Render("_")
}
