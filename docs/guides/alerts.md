---
title: "Alerts"
description: "Rules that watch the power, the energy per token and the sources, and where their notifications go."
weight: 53
---

An alert rule compares one signal of the agent with a threshold every 15
seconds. While the comparison holds the alert is pending; once it has held
for the time of the rule it fires; when it stops holding it is resolved.
Pending and firing alerts survive a restart of the agent, and every alert
that fired stays in the history as long as the hourly rollups.

## Signals

| Signal | Unit | Labels | Value |
|---|---|---|---|
| `node_power_watts` | W | none | Power of the processor package, memory and GPU in the latest sample |
| `backend_power_watts` | W | `backend` | Power of each backend over the latest usage interval |
| `model_joules_per_token` | J | `backend`, `model` | Energy per generated token of each model over the last 15 minutes, once it generated at least 100 tokens |
| `model_jpt_zscore` | none | `backend`, `model` | Z-score of the energy per token of today against the days before it, as [regressions](../../explanation/regressions/) judge it |
| `source_up` | 1 or 0 | `source` | Whether each energy source is selected and returned values in the latest sample |

A rule judges every series of its signal unless `match` keeps only those
with some labels.

## Rules

```yaml
alerts:
  rules:
    - name: high_joules_per_token
      expr: model_joules_per_token
      op: ">"
      threshold: 5.0
      for: 10m
    - name: efficiency_regression
      expr: model_jpt_zscore
      op: ">"
      threshold: 3.0
      for: 30m
    - name: rapl_down
      expr: source_up
      op: "<"
      threshold: 1
      for: 1m
      match:
        source: rapl
```

`op` is `>`, `>=`, `<` or `<=`; quote it in YAML. `for` is how long the
comparison must hold, such as `30s`, `10m` or `1h`; without it the alert
fires at once. Names are unique. The thresholds above are starting points:
choose those that fit your models and hardware.

Reload the agent to apply the rules, or edit them in the **Alerts** screen
of the [graphical interface](../gui/) with the operator role. The screen
previews the configuration file the rules produce, writes it when the agent
can write its own file, and otherwise gives the file to install. Only
`alerts.rules` changes; the comments and the other keys of the file are
kept.

## Notifications

Notifications are sent when an alert fires and when it is resolved, not
while it is pending.

```yaml
alerts:
  sinks:
    log: true
    journald: false
    syslog: false
    webhook:
      enabled: false
      url: ""
      token_file: ""
```

- `log` writes them to the log of the agent, the default.
- `journald` sends them to the journal with the fields
  `GREENLENS_ALERT_RULE`, `GREENLENS_ALERT_STATE` and
  `GREENLENS_ALERT_VALUE`:
  `journalctl SYSLOG_IDENTIFIER=greenlens GREENLENS_ALERT_STATE=firing`.
- `syslog` sends them to the local syslog daemon through `/dev/log`, with
  the facility `daemon`.
- `webhook` posts them as JSON to a URL that resolves to a loopback,
  private or link-local address; any other address is refused when the
  connection is made, so a name cannot point it outside.

Turning on `journald` or `syslog`, or changing `webhook.token_file`, takes
effect after a restart, since the agent confines its own file access when
it starts.

## The webhook

The token file holds one line. The agent sends it as
`Authorization: Bearer <token>` and signs every body with HMAC-SHA256 keyed
with it: `X-GreenLens-Timestamp` carries the Unix time of the request and
`X-GreenLens-Signature` carries `sha256=` and the hex HMAC of the timestamp,
a dot and the body. A receiver checks it with:

```bash
printf '%s.%s' "$timestamp" "$body" | openssl dgst -sha256 -hmac "$token"
```

and refuses requests whose timestamp is too old. The body is the alert:

```json
{
  "schema_version": 1,
  "agent_version": "1.0.0",
  "id": 12,
  "rule": "high_joules_per_token",
  "expr": "model_joules_per_token",
  "op": ">",
  "threshold": 5,
  "for_s": 600,
  "labels": {"backend": "ollama-11434", "model": "qwen2.5:7b"},
  "state": "firing",
  "value": 6.21,
  "started_at": "2026-10-21T15:00:00Z",
  "fired_at": "2026-10-21T15:10:00Z"
}
```

A delivery that fails is tried three more times, 5 seconds, 30 seconds and
a minute apart.

## From the command line

`greenlens status` lists the alerts that are firing or pending.
`greenlens export alerts --from 30d` writes them and the history of the
period as CSV, or as JSON with `--output json`. `greenlens top` shows them
in its fifth view.

## With Prometheus and Alertmanager

`deploy/prometheus/rules.yml` holds rules for Prometheus with the same
signals, for those who route alerts through Alertmanager. The agent exports
`greenlens_model_jpt_zscore` and `greenlens_alerts_firing`, the alerts
firing for each rule, for them.
