---
title: "How energy is measured"
description: "Energy sources, how one is chosen per domain, and what quality means."
weight: 20
---

GreenLens reads energy from the interfaces of the Linux kernel and chooses,
for every part of the machine, one source, so the same energy is never
counted twice.

## Domains

| Domain | What it covers | Combines |
|---|---|---|
| CPU package | One processor socket | added to the node |
| CPU cores, uncore | Parts of a package | contained in the package, not added |
| Memory | DRAM of a socket | added to the node |
| GPU | One graphics card | added to the node |
| Platform (psys), platform, wall | The whole system or more | include the components, never added |

The energy of the node is the sum of the packages, the memory and the GPUs.
Platform and wall measurements serve to see what the rest of the system uses.

## Sources and priority

| Priority | Source | Domains | Quality |
|---|---|---|---|
| 1 | powercap RAPL | package, cores, uncore, memory, psys | measured |
| 2 | perf_event `power` | package, memory, psys | measured |
| 3 | hwmon processor drivers | package, memory | measured |
| 3 | hwmon GPU drivers: amdgpu, xe, i915 | GPU, or the package of an AMD APU | measured |
| 4 | ACPI power meter, IPMI DCMI, battery | platform | platform |
| 6 | Estimate model | package | estimated |

powercap and perf_event read the same hardware counters; GreenLens uses one.
`greenlens doctor` shows every channel found, whether it is used, and why
not. `sources.disable` and `sources.force` change the choice.

## GPUs

A source is chosen for each card, identified by its PCI address, so a
machine with cards of different vendors measures all of them.

- **amdgpu** reports power in watts (`power1_average`, or `power1_input` when
  the firmware has no average). On an APU, a processor with integrated
  graphics, the sensor covers the CPU too, so GreenLens reports it as the
  package and uses it only when RAPL and perf_event cannot be read.
- **xe** reports the energy of the card, or of the GPU package where the
  card counter is missing.
- **i915** reports the energy of the card.

Intel drivers register these sensors only for discrete cards, whose energy is
not part of the processor package. A card in runtime suspend is not read,
because reading would wake it; its power is zero until it resumes.

## From readings to energy

Counters report cumulative energy. GreenLens subtracts consecutive readings
and, when a counter wraps to zero, adds the distance to its maximum
(`max_energy_range_uj`). On a processor drawing 200 W, a counter with a range
of 65 kJ wraps every five minutes, far longer than the one-second sampling
interval; `GL1008` warns when an interval is too long for the power seen.

Power sensors report watts; GreenLens integrates them with the trapezoidal
rule. Intervals are timed with the monotonic clock, so changes of the system
clock do not distort them.

## Quality

Every value carries its quality:

- **measured**: from a hardware energy counter or power sensor of the
  component;
- **platform**: from a meter of the whole system, which includes parts that
  no other source covers, such as fans and disks;
- **estimated**: from the model, when nothing can be read.

A number is never shown without its unit, method and quality.
