---
title: "Read a fleet of agents with mutual TLS"
description: "Create the certificate authority of a fleet, give each agent a certificate and read them all from one place."
weight: 58
---

Each machine runs its own agent. To see several of them together, a
central reader, the hub, collects their metrics over the internal network.
Mutual TLS protects that path in both directions: the agent proves which
node it is, and only the hub may read it. No token travels and none has to
be copied to every node.

## The authority of the fleet

Create it once, on a machine that is not a node:

```bash
greenlens hub init-ca --dir /etc/greenlens/fleet-ca
```

This writes `ca.crt` and `ca.key`. The certificate goes to every agent and
to the hub. The key stays where it was created: whoever has it can issue
certificates the whole fleet trusts. The authority is valid for five years
unless `--days` says otherwise, and the command never replaces an existing
one.

## A certificate for each agent

```bash
greenlens hub issue-cert --ca-dir /etc/greenlens/fleet-ca --out ./certs \
  --name node-a --host 10.0.0.11 --host node-a.example.org
```

`--name` identifies the node. `--host` lists every address and host name
the hub uses to reach it; the hub refuses an agent whose certificate does
not name the address it connected to. Copy `node-a.crt`, `node-a.key` and
`ca.crt` to the node, readable only by the user of the agent, and name
them in its configuration:

```yaml
server:
  listen: 10.0.0.11:10060
  tls:
    cert_file: /etc/greenlens/node-a.crt
    key_file: /etc/greenlens/node-a.key
    client_ca_file: /etc/greenlens/ca.crt
```

With `client_ca_file` set the agent may listen beyond loopback without any
access token: the certificate of the hub is the way in. A client that shows
it reads the API with the viewer role, so it can read measurements and
change nothing. Clients without a certificate, such as a browser, still
need a token or a session.

The systemd unit denies every address but loopback. Copy the
`deploy/systemd/greenlens-network.conf` drop-in and list the address of
the hub in it.

## A certificate for the hub

```bash
greenlens hub issue-cert --ca-dir /etc/greenlens/fleet-ca --out ./certs \
  --name hub --role hub
```

The two roles are not interchangeable. An agent certificate only
identifies a server and a hub certificate only identifies a client, so a
node that is taken over cannot use its own certificate to read the others.

## Run the hub

`greenlens hub` reads the agents listed in its configuration every
`interval` and shows them together on a loopback address:

```yaml
hub:
  listen: 127.0.0.1:10061
  interval: 15s
  tls:
    cert_file: /etc/greenlens/hub.crt
    key_file: /etc/greenlens/hub.key
    ca_file: /etc/greenlens/ca.crt
  nodes:
    - name: node-a
      endpoint: https://10.0.0.11:10060
    - name: node-b
      endpoint: https://10.0.0.12:10060
```

```bash
greenlens hub --config /etc/greenlens/hub.yaml
```

The name of a node must be the name of its certificate. The hub refuses
an agent whose certificate the authority did not sign, one that answers
with the certificate of another node, and one whose certificate does not
name the address of `endpoint`.

| Path | Answers | Access |
|---|---|---|
| `/metrics` | every series of every node that answers, with a `node` label, and the state of each node as `greenlens_hub_node_up` | open on loopback |
| `/api/v1/fleet` | each node with its state, version, power and number of models, and the power of the fleet | access token |
| `/api/v1/fleet/energy` | the energy of each node over a period, hour by hour and domain by domain; `from` and `to` in RFC 3339, the last 24 hours without them | access token |
| `/healthz` | whether the hub runs | open |

The hub uses the access tokens of the machine it runs on, with any role:
it only reads.

```bash
greenlens token create --config /etc/greenlens/hub.yaml --name fleet-panel --role viewer
curl -H "Authorization: Bearer $GREENLENS_TOKEN" http://127.0.0.1:10061/api/v1/fleet
```

```json
{
  "schema_version": 1,
  "time": "2026-11-15T21:50:00Z",
  "nodes_up": 1,
  "power_w": 70.8,
  "nodes": [
    {"name": "node-a", "endpoint": "https://10.0.0.11:10060", "up": true,
     "last_seen": "2026-11-15T21:49:55Z", "version": "v1.0.0", "power_w": 70.8,
     "power_by_domain": {"core": 9.2, "gpu": 15.8, "package": 55.0}, "models": 2},
    {"name": "node-b", "endpoint": "https://10.0.0.12:10060", "up": false,
     "error": "dial tcp 10.0.0.12:10060: connect: connection refused", "power_w": 0, "models": 0}
  ]
}
```

The power and the energy of a node add its processors, memory and GPUs.
The cores are part of the processor and a platform or wall reading already
includes them all, so those domains are shown and never added.

The hub keeps the energy of each node in `hub.db`, in its data directory,
as long as `retention.rollup_1h`. It measures energy between two readings
of the counters of an agent, so the time a node or the hub was down is not
counted for that node, and an agent that restarts starts a new count. A
node that stops answering disappears from `/metrics`, except for
`greenlens_hub_node_up`, which is then 0.

The hub listens on loopback only. Reach it from another machine through
an SSH tunnel, or let the Prometheus of that machine scrape it.

## Read the fleet with Prometheus

Prometheus can scrape the hub, whose series already carry the `node`
label, or the agents directly. A Prometheus that holds the hub certificate
scrapes every agent, and the [Grafana dashboard](../prometheus-grafana/)
shows them together: its Node variable selects one node or adds them.

```yaml
scrape_configs:
  - job_name: greenlens
    scheme: https
    tls_config:
      ca_file: /etc/prometheus/greenlens/ca.crt
      cert_file: /etc/prometheus/greenlens/hub.crt
      key_file: /etc/prometheus/greenlens/hub.key
    static_configs:
      - targets: ["10.0.0.11:10060", "10.0.0.12:10060", "10.0.0.13:10060"]
```

The same three files work with `curl` to check one agent by hand:

```bash
curl --cacert ca.crt --cert hub.crt --key hub.key https://10.0.0.11:10060/api/v1/status
```

An [OpenTelemetry collector](../opentelemetry/) is the other way to gather
a fleet: each agent pushes its metrics to it, and the collector needs no
certificate of the fleet.

## Renew the certificates

Certificates last 30 days unless `--days` says otherwise, up to 366. A
short life limits what a copied key is worth, and there is no revocation
list to maintain: a certificate that should stop working is simply not
renewed.

To renew, issue the same name again and copy the two files over the old
ones:

```bash
greenlens hub issue-cert --ca-dir /etc/greenlens/fleet-ca --out ./certs \
  --name node-a --host 10.0.0.11 --host node-a.example.org
```

The agent notices the new files within seconds and serves the new
certificate without a restart; a pair that does not match yet, while one
file is copied before the other, is tried again. After replacing the hub
certificate, reload Prometheus so it reads the new files. Renew at two
thirds of the life, for example every 20 days from a scheduled job on the
machine that keeps the authority.

The date a certificate expires is in the output of `issue-cert`, also as
JSON with `--output json`, and in the certificate itself:

```bash
openssl x509 -in node-a.crt -noout -enddate
```

## Replace the authority

When the authority nears its end, create a new one in another directory,
issue new certificates from it and switch the nodes one at a time. During
the change an agent trusts one authority, so switch `client_ca_file`, the
certificate and the key of a node together with the files the hub uses
for it.
