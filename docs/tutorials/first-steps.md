---
title: "First steps"
description: "Build GreenLens from source, run it and read its help and errors."
weight: 10
---

This tutorial builds GreenLens from source and shows the parts of the command
line that every later feature relies on: help, languages and error messages.
It takes about five minutes.

## What you need

- A Linux machine on amd64 or arm64.
- Go at the version declared in `go.mod`, git and make.

## 1. Build the binary

```bash
git clone https://github.com/MarioUrenaGarcia/greenlens.git
cd greenlens
make build
```

The binary is written to `bin/greenlens`. It is statically linked, so you can
copy it to any Linux machine of the same architecture.

## 2. Check the build

```bash
bin/greenlens version
```

The output shows the version, the source revision, the Go version and the
platform of the binary.

## 3. Read the help

```bash
bin/greenlens --help
```

Every command has its own help:

```bash
bin/greenlens help version
```

## 4. Switch the language

GreenLens follows the language of your system. To choose it explicitly:

```bash
bin/greenlens --lang es --help
```

## 5. Read an error

Mistype a command on purpose:

```bash
bin/greenlens verson
```

```text
error GL0002: unknown command 'verson'
  cause: 'verson' is not a command of 'greenlens'
  fix:   did you mean 'version'? Run 'greenlens --help' to list the available commands
```

Every error has a code, a cause and a fix. The
[error code reference](../../reference/errors/) lists all of them.
