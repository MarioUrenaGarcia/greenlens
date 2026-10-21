---
title: "Watch the node live"
description: "Follow the power of the node, its backends and its sources in the terminal."
weight: 35
---

`greenlens top` shows what the agent measures, refreshed every second.

```bash
greenlens top
greenlens top --refresh 5s
```

| View | Key | Shows |
|---|---|---|
| Node | `1` | the power of every domain over the last minutes, and the energy since the agent started |
| Backends | `2` | every backend and model: power, energy, energy per generated token, tokens per second, CO2e and, with a tariff, cost |
| Consumers | `3` | the energy and tokens of each consumer identified by the proxy over the last day |
| Sources | `4` | every energy channel, whether it is used and why not, and the problems found |

| Key | Action |
|---|---|
| `Tab`, `Shift+Tab`, `1` to `4` | change the view |
| `Up`, `Down`, `j`, `k` | move in a table |
| `s` | change the sort column |
| `/` | filter the rows; `Enter` keeps the filter, `Esc` clears it |
| `p` | pause the refresh |
| `e` | export the view to a CSV file in the current directory |
| `?` | help of the view |
| `q` | quit |

It works over SSH from 80 by 24 characters. When the locale is not UTF-8,
or with `--ascii`, it draws with ASCII only; with `--no-color` or
`NO_COLOR` it draws without color and tells the lines of the chart apart by
their marks. The bottom right corner shows the CPU and memory `top` itself
uses.
