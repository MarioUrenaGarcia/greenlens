#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Mario Ureña García
#
# Requires the SPDX license identifier and a copyright line within the first
# lines of every Go, TypeScript, JavaScript, CSS and shell file and of the
# Makefile.
#
# Usage: scripts/check-headers.sh [file...]
set -euo pipefail
# shellcheck source=scripts/lib.sh
. "$(dirname "$0")/lib.sh"
export LC_ALL=C.UTF-8

status=0
while IFS= read -r f; do
  case $f in
    *.go | *.ts | *.tsx | *.js | *.css | *.sh | Makefile | */Makefile) ;;
    *) continue ;;
  esac
  head=$(head -n 6 "$f")
  if ! grep -q 'SPDX-License-Identifier: AGPL-3.0-only' <<<"$head"; then
    echo "$f: missing 'SPDX-License-Identifier: AGPL-3.0-only' in the first 6 lines"
    status=1
  fi
  if ! grep -qE 'Copyright \(C\) [0-9]{4}(-[0-9]{4})? [^ ]' <<<"$head"; then
    echo "$f: missing 'Copyright (C) <year> <holder>' in the first 6 lines"
    status=1
  fi
done < <(list_files "$@")
exit $status
