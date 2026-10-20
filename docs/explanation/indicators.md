---
title: "Emissions, cost and water"
description: "How energy becomes kWh, gCO2e, cost and liters, and where the factors come from."
weight: 40
---

```text
kWh   = joules / 3,600,000
gCO2e = kWh x emission factor x PUE
cost  = kWh x tariff x PUE
water = kWh x WUE
```

## Emission factor

By default GreenLens uses the emission factor of the Mexican National
Electric System (FESEN) for 2024: 0.444 tCO2e/MWh, that is 444 gCO2e/kWh,
published by SEMARNAT on 2025-02-28 for the National Emissions Registry. A
SEMARNAT notice of 2026-05-29 keeps it in force while the 2025 factor is
estimated. Elsewhere, set the factor of your grid and its source:

```yaml
indicators:
  emission_factor_g_per_kwh: 233
  emission_factor_source: "Grid operator, 2025 annual factor"
```

## PUE, tariff and water

`indicators.pue` accounts for the overhead of the facility, such as cooling;
1.0 means none. There is no default tariff, because electricity prices vary
by tariff, region, month and hour; set `indicators.tariff.price_per_kwh` from
your bill to see costs. Water is reported only when `indicators.wue_l_per_kwh`
is set.
