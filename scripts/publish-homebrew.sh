#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 7 ]]; then
  echo "usage: publish-homebrew.sh <source-repository> <tag> <version> <formula> <source-manifest> <asset-directory> <hextapctl>" >&2
  exit 64
fi

source_repository="$1"
tag="$2"
version="$3"
formula="$4"
source_manifest="$5"
asset_dir="$(cd -- "$6" && pwd)"
hextapctl="$7"
tap_repository="SijanC147/homebrew-hextap"

[[ "$source_repository" =~ ^SijanC147/[A-Za-z0-9_.-]+$ ]]
[[ "$tag" == "v$version" ]]
[[ "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]
[[ "$formula" =~ ^[a-z][a-z0-9]*(-[a-z][a-z0-9]*)*$ ]]
[[ -f "$source_manifest" && ! -L "$source_manifest" ]]
[[ -x "$hextapctl" ]]
[[ -n "${GH_TOKEN:-}" ]]

workspace="$(mktemp -d)"
trap 'find "$workspace" -depth -delete' EXIT
gh auth setup-git >/dev/null

status=""
tap_commit=""
last_push_output=""
for attempt in 1 2 3; do
  attempt_dir="$workspace/attempt-$attempt"
  gh repo clone "$tap_repository" "$attempt_dir" -- --branch main --depth 1 >/dev/null
  manifest="$attempt_dir/Projects/$formula.json"
  formula_path="$attempt_dir/Formula/$formula.rb"
  [[ -f "$manifest" && ! -L "$manifest" ]] || {
    echo "tap project is not registered: Projects/$formula.json" >&2
    exit 1
  }

  ruby -rjson -e '
    source=JSON.parse(File.read(ARGV.fetch(0)))
    registered=JSON.parse(File.read(ARGV.fetch(1)))
    abort "tap/source manifest mismatch" unless source == registered
  ' "$source_manifest" "$manifest"

  registered_repo="$(ruby -rjson -e 'j=JSON.parse(File.read(ARGV[0])); puts "#{j.dig("formula","repository","owner")}/#{j.dig("formula","repository","name")}"' "$manifest")"
  [[ "$registered_repo" == "$source_repository" ]] || {
    echo "tap registration repository mismatch" >&2
    exit 1
  }
  registered_formula="$(ruby -rjson -e 'puts JSON.parse(File.read(ARGV.fetch(0))).fetch("formula").fetch("name")' "$manifest")"
  [[ "$registered_formula" == "$formula" ]] || {
    echo "tap registration formula mismatch" >&2
    exit 1
  }

  IFS=$'\t' read -r arm64_asset amd64_asset <<< "$(ruby -rjson -e '
    assets=JSON.parse(File.read(ARGV.fetch(0))).fetch("formula").fetch("assets")
    puts [assets.fetch("darwin_arm64"), assets.fetch("darwin_amd64")].join("\t")
  ' "$manifest")"
  [[ "$arm64_asset" =~ ^[A-Za-z0-9][A-Za-z0-9._+-]*\.tar\.gz$ ]]
  [[ "$amd64_asset" =~ ^[A-Za-z0-9][A-Za-z0-9._+-]*\.tar\.gz$ ]]
  [[ "$(basename -- "$arm64_asset")" == "$arm64_asset" ]]
  [[ "$(basename -- "$amd64_asset")" == "$amd64_asset" ]]
  [[ "$arm64_asset" != "$amd64_asset" ]]

  checksums="$asset_dir/SHA256SUMS"
  [[ -f "$checksums" && ! -L "$checksums" ]]
  arm64_sha="$(awk -v file="$arm64_asset" '$2 == file { print $1 }' "$checksums")"
  amd64_sha="$(awk -v file="$amd64_asset" '$2 == file { print $1 }' "$checksums")"
  [[ "$arm64_sha" =~ ^[0-9a-f]{64}$ && "$amd64_sha" =~ ^[0-9a-f]{64}$ ]]

  if [[ -f "$formula_path" ]]; then
    "$hextapctl" formula update --manifest "$manifest" --formula "$formula_path" \
      --version "$version" --arm64-sha "$arm64_sha" --amd64-sha "$amd64_sha"
  else
    mkdir -p "$(dirname "$formula_path")"
    "$hextapctl" formula render --manifest "$manifest" --output "$formula_path" \
      --version "$version" --arm64-sha "$arm64_sha" --amd64-sha "$amd64_sha"
  fi

  git -C "$attempt_dir" add "Formula/$formula.rb"
  if git -C "$attempt_dir" diff --cached --quiet; then
    status="already-current"
    tap_commit="$(git -C "$attempt_dir" rev-parse HEAD)"
    break
  fi

  changed="$(git -C "$attempt_dir" diff --cached --name-only)"
  [[ "$changed" == "Formula/$formula.rb" ]]
  git -C "$attempt_dir" config user.name "GitHub Actions"
  git -C "$attempt_dir" config user.email "actions@github.com"
  git -C "$attempt_dir" commit -m "Update $formula to $version" >/dev/null
  tap_commit="$(git -C "$attempt_dir" rev-parse HEAD)"
  if push_output="$(git -C "$attempt_dir" push origin HEAD:main 2>&1)"; then
    status="published"
    break
  else
    push_status=$?
  fi
  push_diagnostic="${push_output,,}"
  case "$push_diagnostic" in
    *"fetch first"* | *"non-fast-forward"*)
      last_push_output="$push_output"
      ;;
    *)
      printf '%s\n' "$push_output" >&2
      exit "$push_status"
      ;;
  esac
  tap_commit=""
  if (( attempt < 3 )); then
    sleep "$attempt"
  fi
done

[[ -n "$tap_commit" ]] || {
  echo "tap main moved during all publication attempts" >&2
  [[ -z "$last_push_output" ]] || printf '%s\n' "$last_push_output" >&2
  exit 1
}

run_id=""
run_url=""
run_event=""
run_events=(push)
if [[ "$status" == "already-current" ]]; then
  run_events+=(workflow_dispatch)
fi
for _ in {1..60}; do
  for candidate_event in "${run_events[@]}"; do
    run_json="$(gh api --method GET "repos/$tap_repository/actions/workflows/tests.yml/runs" \
      -f event="$candidate_event" -f branch=main -f head_sha="$tap_commit" -f per_page=10)"
    count="$(ruby -rjson -e 'puts JSON.parse(STDIN.read).fetch("workflow_runs").length' <<<"$run_json")"
    if (( count >= 1 )); then
      IFS=$'\t' read -r run_id run_url <<<"$(ruby -rjson -e 'r=JSON.parse(STDIN.read).fetch("workflow_runs").first; puts [r.fetch("id"),r.fetch("html_url")].join("\t")' <<<"$run_json")"
      run_event="$candidate_event"
      break 2
    fi
  done
  sleep 2
done

[[ -n "$run_id" ]] || {
  echo "no exact tap CI run appeared for $tap_commit" >&2
  exit 1
}

gh run watch "$run_id" --repo "$tap_repository" --exit-status
run_state="$(gh api --method GET "repos/$tap_repository/actions/runs/$run_id")"
ruby -rjson -e '
  run=JSON.parse(STDIN.read)
  abort "tap run identity mismatch" unless run.dig("repository","full_name") == "SijanC147/homebrew-hextap"
  abort "tap run path mismatch" unless run.fetch("path") == ".github/workflows/tests.yml"
  abort "tap run commit mismatch" unless run.fetch("head_sha") == ARGV[0]
  abort "tap run event mismatch" unless run.fetch("event") == ARGV[1]
  abort "tap run failed" unless run.fetch("status") == "completed" && run.fetch("conclusion") == "success"
' "$tap_commit" "$run_event" <<<"$run_state"

if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  {
    echo "homebrew_status=$status"
    echo "tap_commit_sha=$tap_commit"
    echo "tap_run_id=$run_id"
    echo "tap_run_url=$run_url"
  } >>"$GITHUB_OUTPUT"
fi
echo "$status $formula $version: $run_url"
