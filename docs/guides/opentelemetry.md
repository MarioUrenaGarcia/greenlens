---
title: "Export metrics with OpenTelemetry"
description: "Send the metrics of the agent to an OpenTelemetry collector with OTLP, over gRPC or HTTP."
weight: 56
---

The agent answers Prometheus scrapes at `/metrics`. It can also push the
same metrics to an OpenTelemetry collector with OTLP, which suits sites
whose pipeline starts at a collector. The export is off by default, like
every outbound connection of GreenLens.

## Turn the export on

```yaml
export:
  otlp:
    enabled: true
    endpoint: http://127.0.0.1:4317
    protocol: grpc
    interval: 15s
```

| Key | Meaning |
|---|---|
| `endpoint` | URL of the collector. It must resolve to a loopback, private or link-local address; the agent checks the address at every connection and refuses any other. |
| `protocol` | `grpc`, on port 4317 by convention, or `http`, on port 4318, with protobuf bodies. With `http` an endpoint without a path gets `/v1/metrics`. |
| `interval` | Time between two exports, from 1 second to 1 hour. |
| `token_file` | File with a token sent as `Authorization: Bearer`, for collectors that ask for one. |
| `ca_file` | PEM file with the authority that signs the certificate of an `https` collector. Empty uses the authorities of the system. |

An `http` endpoint is sent without TLS, which suits a collector on the same
machine. Use `https` for a collector on another machine. `endpoint`,
`protocol`, `interval` and `enabled` take effect on a reload with `SIGHUP`;
the two files are read when the agent starts, so a new path, or the first
`https` endpoint, takes effect after a restart.

The systemd unit only allows loopback connections. For a collector on
another machine, copy the `deploy/systemd/greenlens-network.conf` drop-in
and list the network of the collector in it.

## What is sent

Every export carries all the series of `/metrics`, with the same names and
labels, so a dashboard or a rule works with either path.

| Prometheus type | OTLP data | Notes |
|---|---|---|
| counter | Sum, cumulative and monotonic | The start time is when the agent started. |
| gauge | Gauge | |
| histogram | Histogram, cumulative | Each bucket holds its own observations. |

Each metric states its unit when its name does: `J`, `W`, `s`, `g`, `L`,
`J/{token}`. The resource has `service.name` (`greenlens`),
`service.version` and `host.name`.

A collector that exposes the metrics to Prometheus again keeps the names:
its exporter does not repeat a unit or `_total` that the name already has.

## Check that it works

The export counts itself among the collectors of the agent:

```console
$ curl -s http://127.0.0.1:10060/metrics | grep otlp
greenlens_collector_errors_total{collector="otlp"} 0
greenlens_collector_duration_seconds_count{collector="otlp"} 12
```

A failed export is logged, at most once every five minutes, with what the
collector answered. The next export sends the current totals, so a
collector that was down misses no energy, only the points in between.

## A minimal collector

This collector configuration receives both protocols on loopback and
prints what arrives:

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 127.0.0.1:4317
      http:
        endpoint: 127.0.0.1:4318
exporters:
  debug:
    verbosity: normal
service:
  pipelines:
    metrics:
      receivers: [otlp]
      exporters: [debug]
```
