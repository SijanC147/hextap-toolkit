#!/usr/bin/env bash

set -euo pipefail

fail() {
  printf '::error title=Validated manifest::%s\n' "$1" >&2
  exit 1
}

if [[ $# -ne 2 ]]; then
  fail "expected manifest path and SHA-256 arguments"
fi

manifest_path="$1"
expected_sha256="$2"
[[ ! -L "$manifest_path" ]] || fail "downloaded manifest must not be a symlink"
[[ -e "$manifest_path" ]] || fail "downloaded manifest is missing"
[[ -f "$manifest_path" ]] || fail "downloaded manifest is not a regular file"
[[ "$expected_sha256" =~ ^[0-9a-f]{64}$ ]] || fail "expected manifest SHA-256 is malformed"
if ! actual_sha256="$(sha256sum "$manifest_path" | cut -d ' ' -f 1)"; then
  fail "downloaded manifest could not be hashed"
fi
[[ "$actual_sha256" == "$expected_sha256" ]] || fail "downloaded manifest SHA-256 does not match"
