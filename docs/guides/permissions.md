---
title: "Read the sensors without root"
description: "Give the agent access to RAPL, perf_event and IPMI without running it as root."
weight: 20
---

GreenLens never runs as root. Since the Platypus side channel
(CVE-2020-8694), the kernel makes the RAPL energy counters readable only by
root, so they have to be shared with the agent explicitly. There are three
ways, in order of preference.

## 1. A group for the powercap counters

```bash
sudo greenlens setup-permissions --apply --user $USER
```

This creates the `greenlens-power` group, installs
`/etc/udev/rules.d/70-greenlens-power.rules`, applies it to the existing
counters and adds the user to the group. The system service runs with that
group as a supplementary group. Users added to the group get access after
their next login.

## 2. CAP_PERFMON for perf_event

When the powercap files cannot be shared, the same counters can be read
through the perf_event `power` PMU with the CAP_PERFMON capability:

```bash
sudo greenlens setup-permissions --apply --perfmon
```

This grants the capability to the binary, and must be repeated after every
update of the binary. For the system service, install
`deploy/systemd/greenlens-perfmon.conf` as a drop-in instead.

## 3. The IPMI device

Servers with a BMC report the power of the whole system through IPMI DCMI:

```bash
sudo greenlens setup-permissions --apply --ipmi
```

The service also needs the `deploy/systemd/greenlens-ipmi.conf` drop-in to
open the device.

## The risk, and how to limit it

Reading energy counters at high resolution can reveal information about what
other processes compute. Give the group only to the agent and to the
administrators who need it. If your security policy requires it, raise
`sources.rapl.min_interval` to lower the time resolution GreenLens accepts.

Every step prints its plan before changing anything, and `greenlens doctor`
shows the result.
