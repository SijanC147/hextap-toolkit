#!/usr/bin/env bash

set -euo pipefail

fail() {
  printf '::error title=Runner temp path::%s\n' "$1" >&2
  exit 1
}

if [[ $# -ne 2 ]]; then
  fail "expected runner OS and runner temp arguments"
fi

runner_os="$1"
runner_temp="$2"
[[ -n "$runner_temp" ]] || fail "runner temp is empty"

case "$runner_os" in
  Windows)
    command -v cygpath >/dev/null 2>&1 || fail "cygpath is required on Windows"
    normalized="$(cygpath -u "$runner_temp")" || fail "failed to normalize Windows runner temp"
    ;;
  Linux | macOS)
    normalized="$runner_temp"
    ;;
  *)
    fail "unsupported runner OS"
    ;;
esac

[[ "$normalized" == /* ]] || fail "normalized runner temp is not an absolute POSIX path"
[[ "$normalized" != *$'\r'* && "$normalized" != *$'\n'* ]] || fail "normalized runner temp contains a line break"
[[ -d "$normalized" ]] || fail "normalized runner temp is not an existing directory"
printf '%s\n' "$normalized"
