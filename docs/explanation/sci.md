---
title: "Software Carbon Intensity"
description: "How GreenLens computes the SCI of ISO/IEC 21031 for the node, each backend, model and consumer, with worked cases."
weight: 42
---

The Software Carbon Intensity of the Green Software Foundation, published
as ISO/IEC 21031:2024, is the carbon of software per functional unit:

```text
SCI = (O + M) / R
O   = E x I, the operational emissions
M   = embodied carbon x (time / lifetime) x share of the hardware
R   = the functional unit
```

## O, the operational emissions

E is the electricity of the processor, memory and GPU, with the overhead of
the facility (`indicators.pue`). I is the intensity of the grid: the
emission factor, or, with an hourly profile, the intensity of each hour.
O is therefore the operational emissions of the other reports.

## M, the embodied carbon

M is the carbon of making the hardware, spread over its life. Set the
figures of the product carbon footprints that makers publish for their
servers and GPUs:

```yaml
indicators:
  sci:
    functional_unit: 1k_generated_tokens
    embodied:
      server_kgco2e: <embodied carbon of the server>
      gpu_kgco2e: <embodied carbon of the GPUs>
      lifetime_years: <years of use>
```

There are no defaults. Without them, the SCI is O / R alone and every
report says which values are missing. A year of lifetime is 365.25 days.

The node carries the whole hardware. A backend, model or consumer carries
the share of the server that its processor and memory energy is of the
node's, and the share of the GPU that its GPU energy is. Energy that no
backend accounts for stays with the node.

## R, the functional unit

`1k_generated_tokens`, the default, counts thousands of generated tokens;
`request` counts requests and `consumer` the consumers the proxy tells
apart. `greenlens sci --unit` and the `unit` parameter of the API choose
another one for a single report.

## Worked cases

These are the figures the tests of GreenLens check. Assume one day of 2.4
kWh of components, 1.5 of them of processor and memory and 0.9 of GPU, a
PUE of 1.2, the factor of 444 gCO2e/kWh, a server of 1,200 kgCO2e and GPUs
of 300 kgCO2e over 4 years, and 50,000 generated tokens.

**The node.** O = 2.4 x 1.2 x 444 = 1,278.72 g. The day is 24 / (4 x 8,766)
of the lifetime, so M = (1,200,000 + 300,000) x 24 / 35,064 = 1,026.69 g.
R = 50, and SCI = (1,278.72 + 1,026.69) / 50 = 46.11 gCO2e per 1,000
tokens.

**A model.** It used 1.71 kWh, 0.9 of processor and memory and 0.81 of GPU,
and generated 40,000 tokens. Its shares are 0.9 / 1.5 = 0.6 of the server
and 0.81 / 0.9 = 0.9 of the GPU. O = 1.71 x 1.2 x 444 = 911.09 g, M =
(1,200,000 x 0.6 + 300,000 x 0.9) x 24 / 35,064 = 677.62 g, and SCI =
(911.09 + 677.62) / 40 = 39.72 gCO2e per 1,000 tokens.

**Without embodied carbon.** The node is 1,278.72 / 50 = 25.57 gCO2e per
1,000 tokens, reported without M.

## Reports and metrics

Reports and `greenlens sci` take the shares of the whole period they cover.
The metric `greenlens_sci_grams_co2e_per_unit` and the `embodied` scope of
`greenlens_emissions_grams_co2e_total` add up M interval by interval since
the agent started, with the shares of each interval, so the counter never
decreases.
