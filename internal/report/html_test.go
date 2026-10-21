// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package report

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/i18n"
	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

var rawKey = regexp.MustCompile(`\b(report|domain|quality)\.[a-z0-9_.]+\b`)

func reports(t *testing.T) (ESGReport, SCIReport) {
	t.Helper()
	kwh := indicators.JoulesPerKWh
	f := indicators.Default()
	v := f.Compute(2 * kwh)
	from := time.Date(2026, 10, 19, 6, 0, 0, 0, time.UTC)
	factors := api.Factors{EmissionGPerKWh: 444, EmissionSource: "FESEN 2024", PUE: 1, Currency: "MXN", TimeZone: "America/Mexico_City"}
	// A model name is text from the backend and must reach the page as
	// text.
	evil := "<script>alert(1)</script>"
	groupsIn := []store.UsageEnergy{{Backend: "ollama", Kind: "ollama", Model: evil, EnergyJ: 2 * kwh, GeneratedTokens: 1000, Indicators: &v}}
	sci := BuildSCI(SCIInput{From: from, To: from.Add(24 * time.Hour), GroupBy: store.ByModel, Unit: indicators.UnitGeneratedTokens, Factors: factors,
		Node: Node{ServerJ: 2 * kwh, Indicators: v, GeneratedTokens: 1000}, Groups: groupsIn})
	esg := BuildESG(ESGInput{From: from, To: from.Add(24 * time.Hour), Now: from.Add(25 * time.Hour), Version: "v1.0.0", Factors: factors,
		Channels: []store.ChannelEnergy{{Domain: source.DomainPackage, Quality: "measured", EnergyJ: 2 * kwh, Indicators: &v},
			{Domain: source.DomainWall, Quality: "platform", EnergyJ: 3 * kwh}},
		Backends: []store.UsageEnergy{{Backend: "ollama", Kind: "ollama", EnergyJ: 2 * kwh, Indicators: &v}}, Models: groupsIn, SCI: sci})
	return esg, sci
}

func TestRenderReports(t *testing.T) {
	esg, sci := reports(t)
	for _, tt := range []struct{ lang, esgTitle, sciTitle string }{
		{"en", "Environmental report", "Software Carbon Intensity"},
		{"es", "Reporte ambiental", "Intensidad de carbono del software"},
	} {
		l, err := i18n.New(tt.lang)
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		if err := RenderESG(&b, esg, l); err != nil {
			t.Fatal(err)
		}
		page := b.String()
		check(t, tt.lang+" esg", page, tt.lang, tt.esgTitle)
		// 2 kWh at 444 gCO2e/kWh, local times of Mexico City.
		if !strings.Contains(page, "888 gCO2e") || !strings.Contains(page, "2026-10-19 00:00") || !strings.Contains(page, "America/Mexico_City") {
			t.Errorf("%s esg: figures missing", tt.lang)
		}
		b.Reset()
		if err := RenderSCI(&b, sci, esg.GeneratedAt, "v1.0.0", l); err != nil {
			t.Fatal(err)
		}
		page = b.String()
		check(t, tt.lang+" sci", page, tt.lang, tt.sciTitle)
		// 888 g over 1 unit of 1,000 tokens.
		if !strings.Contains(page, "SCI = (O + M) / R") || strings.Count(page, "888 gCO2e") < 2 {
			t.Errorf("%s sci: figures missing", tt.lang)
		}
	}
}

func check(t *testing.T, name, page, lang, title string) {
	t.Helper()
	if !strings.HasPrefix(page, "<!doctype html>") || !strings.Contains(page, `<html lang="`+lang+`">`) || !strings.Contains(page, "<title>"+title) {
		t.Errorf("%s: head of the page:\n%.300s", name, page)
	}
	if strings.Contains(page, "<script>") || !strings.Contains(page, "&lt;script&gt;") {
		t.Errorf("%s: a model name was not escaped", name)
	}
	if k := rawKey.FindString(page); k != "" {
		t.Errorf("%s: untranslated key %s", name, k)
	}
	if strings.Contains(page, "SPDX") {
		t.Errorf("%s: template comments reached the page", name)
	}
	// The style in the page is the one StyleHash allows.
	start, end := strings.Index(page, "<style>")+len("<style>"), strings.Index(page, "</style>")
	sum := sha256.Sum256([]byte(page[start:end]))
	if "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'" != StyleHash() {
		t.Errorf("%s: the style does not match its hash", name)
	}
}
