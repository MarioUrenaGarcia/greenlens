---
title: "Virtual machines and hardware without sensors"
description: "How GreenLens estimates CPU energy, and how to improve the estimate."
weight: 50
---

Virtual machines usually expose no energy counter. GreenLens then estimates
the power of the CPU from its utilization:

```text
P = threads x (Pmin + (Pmax - Pmin) x utilization^k)
```

Every value is marked as `estimated`, and `greenlens doctor` reports
`GL1007`.

## Where the coefficients come from

Pmin and Pmax are the power of one hardware thread at 0% and 100%
utilization. GreenLens embeds the per-thread coefficients published by Cloud
Carbon Footprint, derived from SPECpower results, for Intel Xeon and AMD EPYC
generations. The processor is identified by its family and model numbers.
When it is not in the table, GreenLens uses the average of the same source,
0.74 W to 3.5 W per thread, and reports `GL1010`.

Utilization excludes steal time, which the hypervisor gives to other guests.

## Better values for your hardware

If you know the power of your CPU at idle and at full load, for example from
the vendor or from a wall meter, set it:

```yaml
sources:
  estimate:
    idle_watts: 35
    full_watts: 180
    exponent: 1
```

Values from a meter on the same model of machine give the best estimate.
