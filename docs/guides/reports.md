---
title: "Produce environmental and SCI reports"
description: "Write the environmental report and the Software Carbon Intensity of a period, as pages to print or files to keep."
weight: 52
---

GreenLens writes two reports of any period from what the agent stored:

- **Environmental report**: the electricity of the node by domain, backend
  and model, its Scope 2 emissions by the location-based method of the
  GHG Protocol, its cost and water when configured, the measured share of
  the energy, the energy no backend accounts for, and the SCI of the node.
  The market-based method needs the emission factors of the electricity
  supplier and is not reported.
- **SCI report**: the Software Carbon Intensity of the node and of each
  backend, model or consumer, with its operational and embodied parts and
  its functional units. See [Software Carbon Intensity](../../explanation/sci/).

Both follow the hourly factors of the agent: an hourly profile of the grid
and time-of-use prices, when they are configured.

## From the command line

```bash
greenlens report --from 2026-10-01 --to 2026-11-01 --esg
greenlens report --from 2026-10-01 --to 2026-11-01 --esg --html october.html
greenlens sci --from 30d --group model
greenlens sci --from 30d --group model --unit request --html sci.html
```

`--html` writes a page that needs nothing else: open it in a browser to
read it or print it on A4. `--output json`, `csv` and `yaml` give the same
figures for other tools. Pages follow the language of the command.

## From the interface

The reports screen has links to both reports of the period it shows, as
printable pages and as CSV and JSON files.

## From the API

```bash
curl -H "Authorization: Bearer $GREENLENS_TOKEN" \
  "http://127.0.0.1:10060/api/v1/reports?kind=esg&format=html&lang=es&from=2026-10-01T00:00:00-06:00&to=2026-11-01T00:00:00-06:00"
```

`kind` is `esg` or `sci`; `format` is `json`, `csv` or `html`; `group_by`
splits the SCI by backend, model or consumer.

## What the environmental report computes

Assume one day with 1.2 kWh of processor package and 0.3 kWh of memory,
measured, 0.9 kWh of GPU, estimated, a platform meter that read 3.1 kWh, a
PUE of 1.2, the factor of 444 gCO2e/kWh and a price of 2.5 per kWh:

- The components add up to 2.4 kWh; the platform meter measures them again
  and is shown but not added.
- With the overhead, the facility used 2.4 x 1.2 = 2.88 kWh.
- Scope 2 emissions are 2.88 x 444 = 1,278.72 gCO2e, and the cost 2.88 x
  2.5 = 7.20.
- The measured share is 1.5 / 2.4 = 62.5%, since the GPU was estimated.
- With 1.8 kWh attributed to the backends, 0.6 kWh is energy that no
  backend accounts for.

These are the figures the tests of GreenLens check.
