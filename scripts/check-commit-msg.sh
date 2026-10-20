#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Mario Ureña García
#
# Validates the first line of a commit message against Conventional Commits:
# type(optional scope)!: description, at most 72 characters. Merge and revert
# messages written by git are accepted as they are.
#
# Usage: scripts/check-commit-msg.sh <message file>
set -euo pipefail
export LC_ALL=C.UTF-8

subject=$(grep -v '^#' "$1" | head -n 1)
case $subject in
  "Merge "* | "Revert \""*) exit 0 ;;
esac
if ! grep -qE '^(feat|fix|refactor|test|docs|build|ci|chore|perf)(\([a-z0-9._/-]+\))?!?: [^ ].*$' <<<"$subject"; then
  echo "commit message must look like 'type(scope): description'"
  echo "types: feat fix refactor test docs build ci chore perf"
  echo "got: $subject"
  exit 1
fi
if [ "${#subject}" -gt 72 ]; then
  echo "commit subject is ${#subject} characters long; the limit is 72"
  exit 1
fi
