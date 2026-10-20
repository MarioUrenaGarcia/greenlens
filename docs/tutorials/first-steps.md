---
title: "First ten minutes"
description: "Build GreenLens, let it read the energy counters, calibrate and measure a command."
weight: 10
---

This tutorial takes you from the source code to your first measurement. It
takes about ten minutes on a Linux machine with an Intel or AMD processor.

## What you need

- Linux on amd64 or arm64, on bare metal to measure real energy. In a virtual
  machine GreenLens works too, with estimated values.
- Go at the version declared in `go.mod`, git and make.
- An administrator account for one step: giving the agent read access to the
  energy counters.

## 1. Build the binary

```bash
git clone https://github.com/MarioUrenaGarcia/greenlens.git
cd greenlens
make build
```

The binary is written to `bin/greenlens`. It is statically linked, so you can
copy it to any Linux machine of the same architecture.

## 2. See what the machine offers

```bash
bin/greenlens doctor
```

`doctor` lists the energy sources it found, which ones it would use and why
the others are not used. On a fresh machine it usually reports `GL1002`: the
RAPL energy counters are readable only by root.

## 3. Let GreenLens read the counters without root

Look at the plan first; nothing changes yet:

```bash
bin/greenlens setup-permissions --user $USER
```

Then apply it:

```bash
sudo bin/greenlens setup-permissions --apply --user $USER
```

The plan creates the `greenlens-power` group, installs a udev rule that lets
that group read the counters and adds your user to the group. Log out and log
in again so your session gets the new group, then check:

```bash
bin/greenlens doctor
```

The sources table now shows `rapl` as used, with quality `measured`.

## 4. Start the agent

In a second terminal:

```bash
bin/greenlens agent
```

The agent samples energy every second, stores it and serves the other
commands. Check it from the first terminal:

```bash
bin/greenlens status
```

## 5. Calibrate the idle power

Close other programs and leave the machine alone for one minute:

```bash
bin/greenlens calibrate
```

The median of that minute becomes the idle baseline. With the default
`dynamic` policy, GreenLens attributes to processes only the energy above it.

## 6. Measure a command

```bash
bin/greenlens measure -- sh -c 'timeout 10 sha256sum /dev/zero; true'
```

When the command ends, `measure` reports the energy attributed to it, its
average power, its emissions with the FESEN 2024 emission factor and the
quality of the values. The [measure guide](../../guides/measure/) explains
each line.

## 7. Report a period

```bash
bin/greenlens report --from 1h
```

You now have the agent measuring the machine. The
[agent guide](../../guides/agent/) shows how to run it as a system service.
