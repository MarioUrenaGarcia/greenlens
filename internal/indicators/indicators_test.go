// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package indicators

import (
	"math"
	"testing"
)

func TestCompute(t *testing.T) {
	tariff, wue := 2.5, 1.8
	f := Factors{EmissionGPerKWh: 444, PUE: 1.5, TariffPerKWh: &tariff, WUELPerKWh: &wue}
	v := f.Compute(7.2e6) // 2 kWh
	if v.KWh != 2 || math.Abs(v.CO2eG-1332) > 1e-9 || math.Abs(*v.Cost-7.5) > 1e-9 || math.Abs(*v.WaterL-3.6) > 1e-9 {
		t.Fatalf("values %+v cost %v water %v", v, *v.Cost, *v.WaterL)
	}
	d := Default().Compute(3.6e6)
	if d.CO2eG != 444 || d.Cost != nil || d.WaterL != nil {
		t.Fatalf("default %+v", d)
	}
}

func TestValidate(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatal(err)
	}
	neg := -1.0
	for _, f := range []Factors{{EmissionGPerKWh: -1, PUE: 1}, {PUE: 0.9}, {PUE: 1, TariffPerKWh: &neg}, {PUE: 1, WUELPerKWh: &neg}} {
		if f.Validate() == nil {
			t.Errorf("accepted %+v", f)
		}
	}
}
