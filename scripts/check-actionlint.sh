#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 0 ]]; then
  echo "usage: check-actionlint.sh" >&2
  exit 64
fi

command -v actionlint >/dev/null 2>&1 || {
  echo "actionlint is required" >&2
  exit 127
}

version="$(actionlint -version | sed -n '1p')"
set +e
diagnostics="$(actionlint -oneline .github/workflows/*.yml 2>&1)"
status=$?
set -e

# GitHub added concurrency.queue and the job.workflow_* reusable-workflow
# identity fields after actionlint 1.7.12. Until an actionlint release contains
# upstream support, the pinned checker must return nonzero with exactly the
# known schema-lag diagnostics. A clean/no-op result, any new diagnostic, any
# missing expected diagnostic, or a different version fails closed.
if [[ "$version" != "1.7.12" && "$version" != "v1.7.12" ]]; then
  printf '%s\n' "$diagnostics" >&2
  echo "actionlint $version reported unexpected diagnostics" >&2
  exit 1
fi

queue_pattern='unexpected key "queue" for "concurrency" section. expected one of "cancel-in-progress", "group" [syntax-check]'
workflow_pattern='property "workflow_sha" is not defined in object type'
queue_count="$(printf '%s\n' "$diagnostics" | grep -F -c "$queue_pattern" || true)"
workflow_count="$(printf '%s\n' "$diagnostics" | grep -F -c "$workflow_pattern" || true)"
unexpected="$(printf '%s\n' "$diagnostics" | grep -F -v "$queue_pattern" | grep -F -v "$workflow_pattern" || true)"

if [[ $status -eq 0 || "$queue_count" != 1 || "$workflow_count" != 5 || -n "$unexpected" ]]; then
  printf '%s\n' "$diagnostics" >&2
  echo "actionlint 1.7.12 diagnostics differ from the reviewed schema-lag set" >&2
  exit 1
fi

echo "actionlint 1.7.12: accepted exactly 1 concurrency.queue and 5 job.workflow_sha schema-lag diagnostics"
