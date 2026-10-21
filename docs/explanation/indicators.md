---
title: "Emissions, cost and water"
description: "How energy becomes kWh, gCO2e, cost and liters, hour by hour, and where the factors come from."
weight: 40
---

```text
kWh   = joules / 3,600,000
gCO2e = kWh x emission factor x PUE
cost  = kWh x tariff x PUE
water = kWh x WUE
```

The agent computes these indicators itself, hour by hour, and hands them
to every client with the energy: the command line, the terminal view, the
interface, the reports and the metrics all show the same values, those of
the configuration of the agent.

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

## An hourly profile of the grid

The intensity of a grid changes with the hour and the day of the week.
With `indicators.hourly_profile_file`, the energy of each hour takes the
intensity of that hour of the week instead of the single factor. The file
is a CSV with this first line and one line for each of the 168 hours of the
week, with the ISO weekday (1 is Monday, 7 is Sunday) and the local hour
from 0 to 23:

```text
weekday,hour,g_co2e_per_kwh
1,0,<intensity of Monday from 00:00 to 01:00>
1,1,<intensity of Monday from 01:00 to 02:00>
...
7,23,<intensity of Sunday from 23:00 to 24:00>
```

GreenLens ships no profile: use the figures of your grid operator. Hours
are those of `indicators.timezone`, an IANA name such as
`America/Mexico_City`, or the time zone of the system when it is empty.

For example, assume a profile of 400 gCO2e/kWh on Mondays at 10:00 and 500
at 11:00, and a PUE of 1.2. Two kWh used evenly from 10:00 to 12:00 put one
kWh in each hour: (1 x 400 + 1 x 500) x 1.2 = 1,080 gCO2e. From 10:30 to
11:30, half a kWh falls in each hour: (0.5 x 400 + 0.5 x 500) x 1.2 = 540
gCO2e.

## PUE, tariff and water

`indicators.pue` accounts for the overhead of the facility, such as cooling;
1.0 means none. There is no default tariff, because electricity prices vary
by tariff, region, month and hour; set `indicators.tariff.price_per_kwh` from
your bill to see costs. Water is reported only when `indicators.wue_l_per_kwh`
is set.

## Time-of-use prices

`indicators.tariff.periods` lists prices for some weekdays between two
local times of the same day; `price_per_kwh` applies outside them, and
without it the periods must cover the whole week. Overlapping periods are
refused. Monthly consumption tiers and charges per kW of demand are not
modeled.

```yaml
indicators:
  tariff:
    price_per_kwh: <the price outside the periods>
    currency: MXN
    periods:
      - days: [mon, tue, wed, thu, fri]
        from: "18:00"
        to: "22:00"
        price_per_kwh: <the price of the period>
```

For example, assume a price of 1.0 outside the period and 3.0 from 18:00
to 22:00 on weekdays. Four kWh used evenly on a Monday from 17:00 to 21:00
put one kWh at the base price and three in the period: 1 x 1.0 + 3 x 3.0 =
10.0. On a Saturday the same use costs 4 x 1.0 = 4.0. A period that ends at
18:20 splits that hour: one kWh from 18:00 to 19:00 costs (20 x 3.0 + 40 x
1.0) / 60 = 1.67.

## Projections

Comparisons that project the savings of another model have no time of use,
so they take the single emission factor and the price outside the periods.
