#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Mario Ureña García
#
# Checks that every translation catalog uses the catalog grammar (comments,
# [table] headers and key = "string" lines), has no duplicate keys, and has
# exactly the keys and {placeholders} of the English catalog.
#
# Usage: scripts/check-i18n.sh [reference.toml other.toml...]
set -euo pipefail
export LC_ALL=C.UTF-8

dir="$(dirname "$0")/../internal/i18n/locales"
if [ $# -eq 0 ]; then
  set -- "$dir/en.toml" "$dir"/*.toml
fi
ref=$1
shift

# describe prints one "K key" line per key and one "P key {placeholder}" line
# per placeholder, or fails on a line outside the grammar.
describe() {
  awk -v file="$1" '
    /^[ \t]*(#|$)/ { next }
    /^[ \t]*\[[A-Za-z0-9_]+(\.[A-Za-z0-9_]+)*\][ \t]*$/ {
      t = $0; gsub(/^[ \t]*\[|\][ \t]*$/, "", t); table = t; next
    }
    /^[ \t]*[A-Za-z0-9_]+[ \t]*=[ \t]*".*"[ \t]*$/ {
      k = $0; sub(/^[ \t]*/, "", k); sub(/[ \t]*=.*$/, "", k)
      if (table != "") k = table "." k
      print "K " k
      v = $0; sub(/^[^=]*=[ \t]*/, "", v)
      while (match(v, /\{[a-z_]+\}/)) {
        print "P " k " " substr(v, RSTART, RLENGTH)
        v = substr(v, RSTART + RLENGTH)
      }
      next
    }
    { printf "%s:%d: line is not a comment, a [table] or key = \"string\"\n", file, NR > "/dev/stderr"; bad = 1 }
    END { exit bad }
  ' "$1"
}

status=0
check_dups() {
  local dups
  dups=$(grep '^K ' <<<"$2" | sort | uniq -d | sed 's/^K //')
  if [ -n "$dups" ]; then
    printf '%s: duplicate key %s\n' "$1" $dups
    status=1
  fi
}

ref_desc=$(describe "$ref") || exit 1
check_dups "$ref" "$ref_desc"
ref_sorted=$(sort -u <<<"$ref_desc")
for f in "$@"; do
  if [ "$f" -ef "$ref" ]; then
    continue
  fi
  if ! desc=$(describe "$f"); then
    status=1
    continue
  fi
  check_dups "$f" "$desc"
  if ! diff_out=$(diff <(echo "$ref_sorted") <(sort -u <<<"$desc")); then
    printf '%s differs from %s (< only in the reference, > only in this file):\n' "$f" "$ref"
    grep -E '^[<>]' <<<"$diff_out"
    status=1
  fi
done
exit $status
