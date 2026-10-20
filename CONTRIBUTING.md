# Contributing to GreenLens

Thank you for helping. This guide explains how to set up a development
machine, which checks every change must pass and how contributions are
accepted.

## Before you start

Contributions are accepted under the [Contributor License Agreement](CLA.md).
Sign it in the description of your first pull request as explained in its
last section. Security problems are reported privately, as described in
[SECURITY.md](SECURITY.md), not in public issues.

## Development machine

- Linux on amd64 or arm64. Energy measurement can only be tested on bare
  metal, not in virtual machines or WSL.
- Go, at the version declared in `go.mod`.
- git, make and a C compiler (gcc). The compiler is only needed by the race
  detector used in tests; release binaries are built without cgo.

Install the pinned development tools into `$(go env GOPATH)/bin` and enable
the git hooks:

```bash
make tools
make hooks
```

## Checks

Every change must pass:

```bash
make check
```

It runs, in order: `go mod tidy` verification, `go vet`, golangci-lint, the
tests with the race detector, govulncheck, gitleaks, the typography, license
header and translation checks, the tests of those checks, the dependency
license check and the verification that `THIRD_PARTY_NOTICES.md` is current.
CI runs the same target on every pull request.

## Rules for changes

- **Commits** are small, do one thing and follow Conventional Commits in
  English: `feat:`, `fix:`, `refactor:`, `test:`, `docs:`, `build:`, `ci:`,
  `chore:` or `perf:`, with an optional scope, for example
  `feat(i18n): add plural forms`. The commit-msg hook checks the format.
- **Tests** accompany every change of behavior.
- **License header**: every Go, TypeScript, JavaScript, CSS and shell file
  starts with:

  ```
  // SPDX-License-Identifier: AGPL-3.0-only
  // Copyright (C) 2026 Mario Ureña García
  ```

  using the comment syntax of the language.
- **User-visible text** never appears as a literal in the code. Add the
  message to `internal/i18n/locales/en.toml` and `es.toml`, with the same key
  and the same `{placeholders}`, and use it through the localizer.
- **Errors** shown to users have a code in `internal/help` with a message, a
  cause and a fix in both catalogs.
- **Typography**: plain ASCII punctuation. No en or em dashes, typographic
  quotes, ellipsis characters or emoji. `scripts/check-typography.sh` lists
  every rule.
- **Network**: GreenLens makes no outbound connections by default. Any
  integration with an external endpoint is opt-in and documented.
- **Privacy**: the content of prompts and responses is never read, logged or
  stored.

## Dependencies

Go dependencies and vendored assets must use a permissive license: MIT,
BSD-2-Clause, BSD-3-Clause, Apache-2.0 or ISC, and SIL OFL 1.1 for fonts.
GPL, LGPL, AGPL, MPL and SSPL dependencies are not accepted because they
prevent the commercial license. Explain in the pull request why a new
dependency is needed, and pin its version.

After changing Go dependencies, regenerate the notices:

```bash
make notices
```

## Graphical interface

The interface sources live in `web/src` and `web/styles`. They are compiled
with the esbuild Go API, so Node.js is not needed:

```bash
make web
```

The output in `web/dist` is committed; the tests fail if it does not match a
fresh build. JavaScript packages are copied from the npm registry with their
integrity verified:

```bash
scripts/vendor-npm.sh <package> <version> <file inside the package>...
```

The script records the license and the SHA-256 of every file in
`web/third_party/manifest.json`, and the tests verify them.

## Documentation

The documentation lives in `docs/` (English) and `docs/es/` (Spanish), with
the same files in both. Every page change needs both languages.

Pages derived from the code, such as the error code reference, are generated;
`make check` fails if they are stale:

```bash
make docs
```

The animated terminal sessions in the README and on the site are produced by
running the real binary:

```bash
make demo
```

The site published on GitHub Pages is built with Hugo from `site/` and the
documentation. To preview it locally:

```bash
make hugo
make site
```

```bash
$(go env GOPATH)/bin/hugo server --source site
```
