#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Mario Ureña García
#
# Rejects typography the project does not use: en and em dashes, typographic
# quotes, the ellipsis character, non-breaking spaces, emoji and decorative
# symbols, and a list of filler words. Box drawing and braille characters are
# allowed because the terminal interface draws with them.
#
# Usage: scripts/check-typography.sh [file...]
set -euo pipefail
# shellcheck source=scripts/lib.sh
. "$(dirname "$0")/lib.sh"
export LC_ALL=C.UTF-8

symbols='[\x{2013}\x{2014}\x{2018}-\x{201F}\x{2022}\x{2026}\x{00A0}\x{200D}\x{2190}-\x{21FF}\x{2600}-\x{27BF}\x{2B00}-\x{2BFF}\x{FE0F}\x{1F000}-\x{1FAFF}]'
# No word appears spelled out in the pattern, so this file does not match its
# own list.
words='\b(robus[t]|seamles[s](ly)?|leverag(e[sd]?|ing)|cutting-edg[e]|comprehensiv[e]|delv(e[sd]?|ing)|empowe[r](s|ed|ing)?)\b'

status=0
while IFS= read -r f; do
  if out=$(grep -nIP -- "$symbols" "$f"); then
    printf '%s\n' "$out" | sed "s|^|$f:|; s|\$|  <- forbidden character|"
    status=1
  fi
  if out=$(grep -nIPi -- "$words" "$f"); then
    printf '%s\n' "$out" | sed "s|^|$f:|; s|\$|  <- filler word|"
    status=1
  fi
done < <(list_files "$@")
exit $status
