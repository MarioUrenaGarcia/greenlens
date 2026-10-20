#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Mario Ureña García
#
# Tests the check scripts against files that break and follow each rule. The
# offending characters are written as byte escapes at run time, so this file
# itself contains none of them.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
failures=0

expect() {
  local want=$1 desc=$2
  shift 2
  if "$@" >"$tmp/out" 2>&1; then got=pass; else got=fail; fi
  if [ "$got" != "$want" ]; then
    echo "FAIL: $desc: expected $want, got $got"
    sed 's/^/    /' "$tmp/out"
    failures=$((failures + 1))
  fi
}

# Typography.
t="$tmp/typography"
mkdir -p "$t/third_party"
printf 'plain text - with a hyphen\nespa\xc3\xb1ol \xc2\xbfs\xc3\xad? \xc2\xa1claro!\nbox \xe2\x94\x80\xe2\x94\x82 braille \xe2\xa3\xbf\n' >"$t/good.md"
expect pass "ASCII, Spanish letters, box drawing and braille" "$here/check-typography.sh" "$t/good.md"
for name in em_dash:'\xe2\x80\x94' en_dash:'\xe2\x80\x93' curly_quote:'\xe2\x80\x9c' ellipsis:'\xe2\x80\xa6' \
  nbsp:'\xc2\xa0' emoji:'\xf0\x9f\x98\x80' check_mark:'\xe2\x9c\x93' arrow:'\xe2\x86\x92' bullet:'\xe2\x80\xa2' \
  star:'\xe2\xad\x90' variation_selector:'\xef\xb8\x8f'; do
  printf "text ${name#*:} text\n" >"$t/${name%%:*}.md"
  expect fail "${name%%:*} is rejected" "$here/check-typography.sh" "$t/${name%%:*}.md"
done
for w in "robu""st" "Seam""less" "lever""aging" "cutting-""edge" "compre""hensive" "del""ves" "em""powers"; do
  printf 'a %s design\n' "$w" >"$t/word.md"
  expect fail "filler word '$w' is rejected" "$here/check-typography.sh" "$t/word.md"
done
printf 'a robustness test\n' >"$t/longer_word.md"
expect pass "longer words that contain a filler word are accepted" "$here/check-typography.sh" "$t/longer_word.md"
printf 'text \xe2\x80\x94 text\n' >"$t/third_party/vendored.md"
expect pass "third-party files are skipped" "$here/check-typography.sh" "$t/third_party/vendored.md"

# Headers.
h="$tmp/headers"
mkdir -p "$h"
spdx='SPDX-License-Identifier: AGPL-3.0-only'
printf '// %s\n// Copyright (C) 2026 Example Holder\n\npackage x\n' "$spdx" >"$h/good.go"
expect pass "Go file with both header lines" "$here/check-headers.sh" "$h/good.go"
printf '#!/bin/sh\n# %s\n# Copyright (C) 2026-2027 Example Holder\n' "$spdx" >"$h/good.sh"
expect pass "shell script with a shebang and a year range" "$here/check-headers.sh" "$h/good.sh"
printf '// Copyright (C) 2026 Example Holder\npackage x\n' >"$h/no_spdx.go"
expect fail "missing SPDX identifier" "$here/check-headers.sh" "$h/no_spdx.go"
printf '/* %s */\n.a { color: red; }\n' "$spdx" >"$h/no_copyright.css"
expect fail "missing copyright line" "$here/check-headers.sh" "$h/no_copyright.css"
printf '// SPDX-License-Identifier: MIT\n// Copyright (C) 2026 Example Holder\n' >"$h/wrong_license.ts"
expect fail "different license identifier" "$here/check-headers.sh" "$h/wrong_license.ts"
printf '\n\n\n\n\n\n// %s\n// Copyright (C) 2026 Example Holder\n' "$spdx" >"$h/late.go"
expect fail "header after the sixth line" "$here/check-headers.sh" "$h/late.go"
printf '# notes without a header\n' >"$h/notes.md"
expect pass "files without a source extension are skipped" "$here/check-headers.sh" "$h/notes.md"

# Translations.
i="$tmp/i18n"
mkdir -p "$i"
printf '# ref\n[a]\nx = "hello {name}"\ny = "bye"\n' >"$i/en.toml"
printf '[a]\ny = "adios"\nx = "hola {name}"\n' >"$i/same.toml"
expect pass "same keys and placeholders in another order" "$here/check-i18n.sh" "$i/en.toml" "$i/same.toml"
printf '[a]\nx = "hola {name}"\n' >"$i/missing.toml"
expect fail "missing key" "$here/check-i18n.sh" "$i/en.toml" "$i/missing.toml"
printf '[a]\nx = "hola {name}"\ny = "adios"\nz = "extra"\n' >"$i/extra.toml"
expect fail "extra key" "$here/check-i18n.sh" "$i/en.toml" "$i/extra.toml"
printf '[a]\nx = "hola {nombre}"\ny = "adios"\n' >"$i/placeholder.toml"
expect fail "different placeholder" "$here/check-i18n.sh" "$i/en.toml" "$i/placeholder.toml"
printf '[a]\nx = "hola {name}"\ny = adios\n' >"$i/grammar.toml"
expect fail "value outside the grammar" "$here/check-i18n.sh" "$i/en.toml" "$i/grammar.toml"
printf '[a]\nx = "hola {name}"\ny = "adios"\ny = "otra vez"\n' >"$i/duplicate.toml"
expect fail "duplicate key" "$here/check-i18n.sh" "$i/en.toml" "$i/duplicate.toml"

# Commit messages.
c="$tmp/commit"
mkdir -p "$c"
msg() { printf '%s\n' "$1" >"$c/msg"; }
msg "feat: add the version command"
expect pass "type and description" "$here/check-commit-msg.sh" "$c/msg"
msg "fix(i18n)!: reject duplicate keys"
expect pass "scope and breaking change marker" "$here/check-commit-msg.sh" "$c/msg"
msg "Merge branch 'topic'"
expect pass "merge message written by git" "$here/check-commit-msg.sh" "$c/msg"
msg "Add stuff"
expect fail "no type" "$here/check-commit-msg.sh" "$c/msg"
msg "feature: add stuff"
expect fail "unknown type" "$here/check-commit-msg.sh" "$c/msg"
msg "feat:missing space"
expect fail "no space after the colon" "$here/check-commit-msg.sh" "$c/msg"
msg "feat: $(printf 'x%.0s' $(seq 1 70))"
expect fail "subject longer than 72 characters" "$here/check-commit-msg.sh" "$c/msg"

if [ "$failures" -gt 0 ]; then
  echo "$failures check script test(s) failed"
  exit 1
fi
echo "check scripts: all tests passed"
