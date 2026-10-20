---
title: "Measure a command, a process or a service"
description: "Attribute energy to what you run, and read the report."
weight: 40
---

`greenlens measure` attributes to a workload the part of the energy of the
node that matches its CPU time. It works with or without the agent.

## Three ways to choose what to measure

```bash
greenlens measure -- ollama run llama3.2 "Why is the sky blue?"
```

```bash
greenlens measure --pid 4242 --duration 5m
```

```bash
greenlens measure --unit ollama.service --duration 10m
```

A command is measured until it ends, together with every child it starts;
its exit code becomes the exit code of `measure`, and the report goes to
standard error so the output of the command stays clean. A PID is measured
with its children until it ends or `--duration` passes. A systemd unit is
measured through its cgroup, which counts every process of the service.

## Reading the report

| Line | Meaning |
|---|---|
| Attributed energy | CPU and memory energy attributed to the workload |
| Average power | Attributed energy divided by the duration |
| CPU share | CPU time of the workload over the busy time of the machine |
| Energy of the node | All the CPU, memory and GPU energy measured meanwhile |
| Emissions | Attributed energy times the emission factor and the PUE |
| Method and quality | How the value was obtained: measured, platform or estimated |
| Idle baseline | Idle power subtracted under the dynamic policy |

Without a calibration, the dynamic policy has no baseline and attributes all
the energy by CPU share. Run `greenlens calibrate` first for a fair split.

## For scripts

```bash
greenlens measure --report run.json -o json -- ./benchmark.sh
```

The JSON document has a `schema_version` and its fields do not change within
a version.
