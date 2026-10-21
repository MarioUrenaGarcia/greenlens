// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package report

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/i18n"
	"github.com/MarioUrenaGarcia/greenlens/internal/units"
)

//go:embed templates
var files embed.FS

// css is the style of the printable reports, written into each one so a
// saved report needs nothing else. The license header of the file stays
// in the source.
var css = func() string {
	b, err := files.ReadFile("templates/report.css")
	if err != nil {
		// The file is embedded at build time.
		panic(err)
	}
	_, rest, _ := strings.Cut(string(b), "*/")
	return strings.TrimSpace(rest) + "\n"
}()

// style is css marked safe for the template: it is the embedded file of
// the build, never input.
var style = template.CSS(css) //nolint:gosec // embedded stylesheet, see above

// StyleHash is the Content-Security-Policy source that allows the style of
// the reports and nothing else.
func StyleHash() string {
	sum := sha256.Sum256([]byte(css))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

func pages(l *i18n.Localizer, loc *time.Location) *template.Template {
	num := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	funcs := template.FuncMap{
		"t":      func(key string, args ...any) string { return l.T(key, args...) },
		"energy": func(j float64) string { return units.Energy(j, false) },
		"co2e":   func(g float64) string { return units.CO2e(g, false) },
		"kwh":    func(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) + " kWh" },
		"money": func(v float64, currency string) string {
			return strconv.FormatFloat(v, 'f', 2, 64) + " " + currency
		},
		"liters": func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) + " L" },
		"pct":    func(v float64) string { return strconv.FormatFloat(100*v, 'f', 1, 64) + "%" },
		"num":    num,
		"numptr": func(v *float64) string {
			if v == nil {
				return "-"
			}
			return num(*v)
		},
		"date": func(t time.Time) string { return t.In(loc).Format("2006-01-02 15:04") },
		"join": func(s []string) string { return strings.Join(s, ", ") },
	}
	return template.Must(template.New("").Funcs(funcs).ParseFS(files, "templates/*.html"))
}

// location returns the time zone the factors name, or UTC.
func location(f api.Factors) *time.Location {
	if loc, err := time.LoadLocation(f.TimeZone); err == nil && f.TimeZone != "" {
		return loc
	}
	return time.UTC
}

// zoneLabel names a time zone for the reader: its IANA name, or, for a
// zone of the system without one, its abbreviation and offset at t.
func zoneLabel(loc *time.Location, t time.Time) string {
	if name := loc.String(); name != "Local" {
		return name
	}
	abbr, off := t.In(loc).Zone()
	sign := "+"
	if off < 0 {
		sign, off = "-", -off
	}
	return fmt.Sprintf("%s, UTC%s%02d:%02d", abbr, sign, off/3600, off%3600/60)
}

// factorText describes the intensity of the grid that applied.
func factorText(l *i18n.Localizer, f api.Factors) string {
	if f.HourlyProfile {
		return l.T("report.factor_profile", "zone", f.TimeZone)
	}
	return l.T("report.factor", "value", strconv.FormatFloat(f.EmissionGPerKWh, 'f', -1, 64), "source", f.EmissionSource)
}

// RenderESG writes the environmental report as a printable page.
func RenderESG(w io.Writer, rep ESGReport, l *i18n.Localizer) error {
	loc := location(rep.Factors)
	return pages(l, loc).ExecuteTemplate(w, "esg.html", map[string]any{
		"Lang": l.Lang(), "CSS": style, "R": rep, "Zone": zoneLabel(loc, rep.From), "Factor": factorText(l, rep.Factors),
	})
}

// RenderSCI writes the SCI report as a printable page.
func RenderSCI(w io.Writer, rep SCIReport, now time.Time, version string, l *i18n.Localizer) error {
	loc := location(rep.Factors)
	return pages(l, loc).ExecuteTemplate(w, "sci.html", map[string]any{
		"Lang": l.Lang(), "CSS": style, "S": rep, "Zone": zoneLabel(loc, rep.From), "Factor": factorText(l, rep.Factors),
		"Now": now, "Version": version,
	})
}
