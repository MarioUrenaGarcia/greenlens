---
title: "How energy is attributed"
description: "From the energy of the node to the energy of a process, and the idle policies."
weight: 30
---

The hardware measures the node, not processes. GreenLens attributes to each
workload the part of the energy that matches its share of CPU time.

## The formula

For every sampling interval:

```text
P_total    = package energy / interval
P_dynamic  = max(0, P_total - P_idle)
share_b    = CPU time of the workload / busy CPU time of the machine
E_b        = P_dynamic x interval x share_b
```

Memory energy is attributed with the same share. The sum of what is
attributed never exceeds what was measured; the test suite checks this
property on tens of thousands of random cases.

## Idle policies

`attribution.idle_policy` decides what happens to the idle energy:

| Policy | Attributes | Fits |
|---|---|---|
| `dynamic` (default) | only the energy above the idle baseline | shared machines |
| `proportional` | also the idle energy, by CPU share | showback that includes idle cost |
| `full` | all the energy to the active workloads | nodes dedicated to inference |
| `wall` | the platform or wall energy above its idle, by share | facilities that report at the plug |

## The idle baseline

`greenlens calibrate` measures the machine at rest for a minute and stores
the median of each domain, with the CPU frequency, the governor and the
temperature. While it runs, the agent keeps the 5th percentile of the power
of the last 24 hours as the current idle estimate, and warns when it moves
more than 20% away from the calibration, which happens after hardware or
firmware changes.
