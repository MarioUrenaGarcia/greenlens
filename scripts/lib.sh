# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Mario Ureña García
#
# Shared helpers for the check scripts. Sourced, not executed.

# Paths the checks skip: verbatim third-party texts and generated files whose
# content the project does not author.
GREENLENS_EXCLUDE='^(LICENSE|THIRD_PARTY_NOTICES\.md|go\.sum)$|(^|/)third_party/|^web/dist/|(^|/)testdata/'

# list_files prints the files to check, one per line: the arguments when
# given (as lefthook passes staged files), otherwise every tracked or
# untracked file that git does not ignore. Excluded and missing paths are
# dropped in both cases.
list_files() {
  if [ $# -gt 0 ]; then
    printf '%s\n' "$@"
  else
    git ls-files --cached --others --exclude-standard
  fi | grep -Ev "$GREENLENS_EXCLUDE" | while IFS= read -r f; do
    if [ -f "$f" ]; then
      printf '%s\n' "$f"
    fi
  done
}
