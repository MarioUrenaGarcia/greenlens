---
title: "Integrate with Prometheus and Grafana"
description: "Scrape the agent with Prometheus, load the alerting rules and show the Grafana dashboard."
weight: 54
---

The agent serves its metrics in the Prometheus text format at `/metrics`
of its HTTP API, on `127.0.0.1:10060` by default. The names start with
`greenlens_` and use base units: joules, watts, seconds, grams and liters.

## Scrape the agent

```yaml
scrape_configs:
  - job_name: greenlens
    static_configs:
      - targets: ["127.0.0.1:10060"]
```

`/metrics` needs no token unless `server.metrics_auth` is on. With it on,
create a token for Prometheus and name its file in the scrape
configuration:

```bash
greenlens token create --name prometheus --role viewer
```

```yaml
scrape_configs:
  - job_name: greenlens
    authorization:
      credentials_file: /etc/prometheus/greenlens-token
    static_configs:
      - targets: ["127.0.0.1:10060"]
```

A Prometheus on another machine needs the agent to listen on an internal
address, which it only does with at least one access token created; see
[the HTTP API](../http-api/).

## What to query

| Question | Query |
|---|---|
| Power of the node | `sum(greenlens_node_power_watts{domain=~"package\|dram\|gpu"})` |
| Energy of the node over a day, in kWh | `sum(increase(greenlens_node_energy_joules_total{domain=~"package\|dram\|gpu"}[1d])) / 3.6e6` |
| Power of each backend | `sum by (backend) (rate(greenlens_backend_energy_joules_total[5m]))` |
| Energy per generated token of each model | `sum by (backend, model) (rate(greenlens_model_energy_joules_total[15m])) / sum by (backend, model) (rate(greenlens_model_tokens_total{type="generated"}[15m]))` |
| Emissions of each model over a day, in grams | `sum by (backend, model) (increase(greenlens_emissions_grams_co2e_total{scope="operational"}[1d]))` |

Add only the `package`, `dram` and `gpu` domains for the energy of the
node. `core` and `uncore` are part of the package, and `psys`, `platform`
and `wall` are readings of the whole machine that already include the
components.

Counters start at zero when the agent starts, as Prometheus expects;
`rate` and `increase` handle the restart.

## Alerting rules

`deploy/prometheus/rules.yml` holds rules for Alertmanager that match the
signals of the [alert rules](../alerts/) of the agent. Copy it next to the
Prometheus configuration and name it there:

```yaml
rule_files:
  - greenlens-rules.yml
```

## The Grafana dashboard

`deploy/grafana/greenlens.json` is a dashboard for a Prometheus data
source. Import it in Grafana from Dashboards, New, Import, and choose the
data source. It shows:

- the power of the node by domain, its energy and its emissions over the
  period, the sources that return no values and the alerts that are firing;
- the power of each backend, and how it splits into processor, memory and
  GPU;
- the energy per generated token and the generated tokens per second of
  each model, and a table with the totals of the period;
- the power of the agent itself and the errors and read times of its
  collectors.

The Node, Backend and Model variables narrow every panel. With several
agents in one Prometheus, Node selects one or adds them.

## A local environment

`deploy/compose/compose.yaml` starts Prometheus, Grafana and an
OpenTelemetry collector that are already configured for the agent of this
machine, to try the integrations:

```bash
docker compose -f deploy/compose/compose.yaml up
```

Grafana answers at `http://127.0.0.1:3000` with the dashboard as its home
page, and Prometheus at `http://127.0.0.1:9090`. Every service listens on
loopback only. To see the [OpenTelemetry export](../opentelemetry/) too,
turn it on with the endpoint `http://127.0.0.1:4317`; the collector hands
what it receives to Prometheus as the job `greenlens-otlp`.
