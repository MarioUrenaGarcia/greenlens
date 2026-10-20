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

## Backends, GPUs and models

Each inference backend is a group of processes: its server and every
descendant. Its CPU time gives its share of the processor and memory energy,
as above.

The energy of a GPU goes to the backends that run on it:

- when the driver reports the use of each process, as NVML does, by that
  use (`attributed_gpu_process`);
- when it reports only which processes hold a context, all of it to the
  only backend there, or in equal parts between several (`estimated`).

Under the `dynamic` policy only the energy above the idle power of each
GPU, measured by `greenlens calibrate`, is attributed.

Every ten seconds the energy of a backend is split between its models by
the tokens each one processed, prompt and generated (`token_weighted`).
A single active model takes all of it. Models beyond
`attribution.max_models_per_backend` are reported together as `other`.

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
