---
title: "Measure the whole machine: Redfish and wall meters"
description: "Read the power of the server from its management controller or from a smart plug, and validate the other sources against it."
weight: 22
---

The counters of the processor and of the GPU leave out the rest of the
machine: the motherboard, the disks, the fans and the losses of the power
supply. Two sources read the whole machine over the network. Both are off
by default, because they make the agent connect to another device, and
both only reach loopback, private or link-local addresses.

| Source | Reads | Domain | Device |
|---|---|---|---|
| `redfish` | power of the chassis, as its controller reports it | platform | management controller (BMC) of the server |
| `wall_meter` | power at the plug | wall | Shelly or Tasmota smart plug |

A platform or wall reading already includes the processor, the memory and
the GPU, so GreenLens never adds it to them. It shows it as its own domain,
uses it to tell how much the rest of the machine draws, and attributes it
with the `wall` [idle policy](../../explanation/attribution/).

The systemd unit only allows loopback connections: copy the
`deploy/systemd/greenlens-network.conf` drop-in and list the address of
the controller or of the meter in it. A change of these settings takes
effect when the agent restarts.

## Redfish

```yaml
sources:
  redfish:
    enabled: true
    endpoint: https://10.0.0.5
    token_file: /etc/greenlens/redfish-token
    ca_file: /etc/greenlens/bmc-ca.pem
    interval: 5s
```

The agent reads `PowerWatts` of the `EnvironmentMetrics` resource of the
chassis, and `PowerControl.PowerConsumedWatts` of the `Power` resource on
controllers that predate it. It uses the first chassis that reports its
power; `chassis` names another, by the last part of its path, such as
`System.Embedded.1`.

`token_file` holds the token of a Redfish session, which the agent sends
as `X-Auth-Token`. Create an account on the controller with the `ReadOnly`
role for it, open a session and keep the token the controller returns in
the `X-Auth-Token` header of its answer:

```bash
curl --cacert /etc/greenlens/bmc-ca.pem -D - -o /dev/null \
  -H 'Content-Type: application/json' \
  -d '{"UserName": "greenlens", "Password": "..."}' \
  https://10.0.0.5/redfish/v1/SessionService/Sessions
```

Write the token alone in the file, readable only by the user of the agent.
A session ends after the idle time of the controller, usually 30 minutes;
the readings every few seconds keep it open. It also ends when the
controller restarts: `greenlens doctor` then reports GL1016 with a 401
answer, and the file needs a new token.

Controllers ship with a certificate signed by themselves. Export it, or
the authority that signs the one you installed, and name it in `ca_file`.
The agent always verifies the certificate.

## A wall meter

```yaml
sources:
  wall_meter:
    enabled: true
    kind: shelly
    endpoint: http://192.168.1.50
    channel: 0
    interval: 2s
```

| Kind | Devices | How it is read |
|---|---|---|
| `shelly` | Shelly plugs and relays with power metering, of any generation | `/rpc/Switch.GetStatus` from the second generation on, `/meter/{channel}` on the first |
| `tasmota` | devices with the Tasmota firmware and an energy sensor | `Status 10` |

`channel` picks the output of a device with several, counted from zero.
The meter must answer without a password, so keep it on a network that
only the machines it measures can reach.

Plug only the measured machine into the meter. A meter that also feeds a
monitor or a switch reports their power as that of the machine.

## Validate the other sources

A wall meter is the reference to check the counters against. With the
machine idle, and then under a steady load, compare the domains over a few
minutes:

```bash
greenlens report --from 10m
```

The wall energy is higher than the sum of the processor, the memory and
the GPU: the difference is the rest of the machine and the losses of the
power supply, typically 10% to 20% of the wall power under load and a
larger share at idle. What validates the counters is that the difference
stays steady while the load changes: when the processor draws 50 W more,
the wall should show 50 W more, plus the losses on those 50 W.

Plug meters report about once a second and round to a tenth of a watt or
more, so compare energies over minutes, not single samples.

## When a source stops answering

The agent reads both in the background, so a device that is slow or down
never delays the other sources. After three intervals without an answer
the domain has no value, instead of repeating the last one, and the
readings resume by themselves when the device answers again. The `source_up`
signal of the [alerts](../alerts/) can watch it:

```yaml
alerts:
  rules:
    - name: wall_meter_down
      expr: source_up
      op: "<"
      threshold: 1
      for: 2m
      match:
        source: wall_meter
```
