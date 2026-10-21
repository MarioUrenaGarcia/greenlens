// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package report

import (
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

// The figures are those worked by hand in the documentation of the report.
func TestBuildESG(t *testing.T) {
	kwh := indicators.JoulesPerKWh
	f := indicators.Default()
	f.PUE = 1.2
	price := 2.5
	f.TariffPerKWh = &price
	ind := func(j float64) *indicators.Values { v := f.Compute(j); return &v }
	from := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	in := ESGInput{
		From: from, To: from.Add(24 * time.Hour), Now: from.Add(25 * time.Hour), Version: "test",
		Factors: api.Factors{EmissionGPerKWh: 444, PUE: 1.2, Currency: "MXN", TariffPerKWh: &price},
		Channels: []store.ChannelEnergy{
			{Channel: "rapl:package-0", Domain: source.DomainPackage, Quality: "measured", EnergyJ: 1.2 * kwh, Indicators: ind(1.2 * kwh)},
			{Channel: "rapl:dram", Domain: source.DomainDRAM, Quality: "measured", EnergyJ: 0.3 * kwh, Indicators: ind(0.3 * kwh)},
			{Channel: "estimate:gpu", Domain: source.DomainGPU, Quality: "estimated", EnergyJ: 0.9 * kwh, Indicators: ind(0.9 * kwh)},
			{Channel: "ipmi:platform", Domain: source.DomainPlatform, Quality: "platform", EnergyJ: 3.1 * kwh, Indicators: ind(3.1 * kwh)},
		},
		Backends: []store.UsageEnergy{{Backend: "ollama", Kind: "ollama", EnergyJ: 1.8 * kwh, GeneratedTokens: 40_000, Indicators: ind(1.8 * kwh)}},
		Models:   []store.UsageEnergy{{Backend: "ollama", Kind: "ollama", Model: "qwen", EnergyJ: 1.8 * kwh, GeneratedTokens: 40_000, Indicators: ind(1.8 * kwh)}},
	}
	rep := BuildESG(in)
	// 2.4 kWh of components; with PUE 1.2 the facility used 2.88 kWh,
	// 2.88 x 444 = 1278.72 gCO2e and 2.88 x 2.5 = 7.20 MXN. 1.5 of the 2.4
	// kWh were measured (0.625), and 0.6 kWh went to no backend.
	if !near(rep.ComponentsJ, 2.4*kwh) || !near(rep.FacilityKWh, 2.88) || !near(rep.Indicators.CO2eG, 1278.72) ||
		rep.Indicators.Cost == nil || !near(*rep.Indicators.Cost, 7.2) || !near(rep.MeasuredShare, 0.625) ||
		!near(rep.UnattributedJ, 0.6*kwh) || rep.Method != MethodScope2Location {
		t.Fatalf("report %+v", rep)
	}
	// Components first, the platform meter last and not added.
	if len(rep.Domains) != 4 || !rep.Domains[0].Component || rep.Domains[3].Domain != source.DomainPlatform || rep.Domains[3].Component {
		t.Fatalf("domains %+v", rep.Domains)
	}
	if m := rep.Models[0]; m.JPerToken != 1.8*kwh/40_000 || !near(m.Indicators.CO2eG, 1.8*1.2*444) {
		t.Fatalf("model %+v", m)
	}
}
