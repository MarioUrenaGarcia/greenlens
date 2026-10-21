---
title: "Use the graphical interface"
description: "Open the interface in the browser, add it to the desktop and read what each screen shows."
weight: 30
---

The agent serves a graphical interface on the same address as its HTTP API,
`127.0.0.1:10060` by default. It needs nothing from the internet: fonts,
charts and this documentation are part of the binary.

## Open it

With the agent running, on the same machine:

```bash
greenlens gui
```

The command asks the agent for a one-time sign-in link and opens it in the
default browser. The link works once, within two minutes. `--role viewer` or
`--role operator` signs the browser in with fewer rights than the default
`admin`, and `--print` prints the link instead of opening it.

Over SSH, `greenlens gui` prints the link with the port forward to run on
your computer:

```bash
ssh -N -L 10060:127.0.0.1:10060 server.example.com
```

From another machine without a tunnel, the agent must listen on the network
and the browser signs in with an access token; see
[Use the HTTP API and access tokens](../http-api/).

When the HTTP API is off (`server.enabled: false`), the command stops with
`GL4015`.

## Add it to the desktop

The launcher runs `greenlens gui`. Install it for your user:

```bash
install -D -m 0644 deploy/desktop/greenlens.desktop ~/.local/share/applications/greenlens.desktop
install -D -m 0644 deploy/desktop/greenlens.svg ~/.local/share/icons/hicolor/scalable/apps/greenlens.svg
```

Started from the launcher, an error appears as a desktop notification when
`notify-send` is installed.

Browsers can also install the interface as an application from their menu,
at `127.0.0.1` or over TLS. The installed window keeps its own files, so it
opens and says that the agent does not answer instead of showing a browser
error. Measurements always come from the agent and are never kept by the
browser.

## Screens

| Screen | Shows |
|---|---|
| Summary | the power of the node now and over the last minutes, by domain, and the energy of the last day, week or month |
| Backends | every inference server found or declared, with its power, tokens per second and energy per token |
| Models | the energy per generated token and the SCI of each model, its history, the split between prefill and decode, and the days that are regressions next to the changes of context |
| Consumers | the energy and tokens of each consumer identified by the proxy |
| Benchmarks | run a benchmark against a backend, compare two results and export them |
| Reports | the energy, emissions, cost and water of a period, by day or hour and by backend, model or consumer, as CSV or JSON, with the printable environmental and SCI reports |
| Sources | every energy channel, whether it is used and why not |
| Configuration | the settings of the agent, with a preview of each change (administrators only) |
| Help | this documentation, with search |
| About | the version, the license, the components and what the interface itself consumes |

The first visit shows a few first steps, including a calibration of idle
power when the agent has none yet.

## Preferences

The language, the theme (as the system, dark or light) and the efficient
mode are kept in the browser. Efficient mode turns off blur and animations
and redraws the charts every five seconds. In automatic mode it turns on
when the device runs on battery, or when the processing of the interface
itself goes over 1 W, as the About screen estimates it.

Every screen works with the keyboard, and charts come with a table of their
values.
