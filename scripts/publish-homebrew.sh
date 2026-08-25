#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 6 ]]; then
  echo "usage: publish-homebrew.sh <source-repository> <tag> <version> <formula> <asset-directory> <hextapctl>" >&2
  exit 64
fi

source_repository="$1"
tag="$2"
version="$3"
formula="$4"
asset_dir="$(cd -- "$5" && pwd)"
hextapctl="$6"
tap_repository="SijanC147/homebrew-hextap"

[[ "$source_repository" =~ ^SijanC147/[A-Za-z0-9_.-]+$ ]]
[[ "$tag" == "v$version" ]]
[[ "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]
[[ "$formula" =~ ^[a-z][a-z0-9]*(-[a-z][a-z0-9]*)*$ ]]
[[ -x "$hextapctl" ]]
[[ -n "${GH_TOKEN:-}" ]]

arm64_asset="$formula-darwin-arm64.tar.gz"
amd64_asset="$formula-darwin-amd64.tar.gz"
arm64_sha="$(awk -v file="$arm64_asset" '$2 == file { print $1 }' "$asset_dir/SHA256SUMS")"
amd64_sha="$(awk -v file="$amd64_asset" '$2 == file { print $1 }' "$asset_dir/SHA256SUMS")"
[[ "$arm64_sha" =~ ^[0-9a-f]{64}$ && "$amd64_sha" =~ ^[0-9a-f]{64}$ ]]

workspace="$(mktemp -d)"
trap 'find "$workspace" -depth -delete' EXIT
gh auth setup-git >/dev/null

status=""
tap_commit=""
for attempt in 1 2 3; do
  attempt_dir="$workspace/attempt-$attempt"
  gh repo clone "$tap_repository" "$attempt_dir" -- --branch main --depth 1 >/dev/null
  manifest="$attempt_dir/Projects/$formula.json"
  formula_path="$attempt_dir/Formula/$formula.rb"
  [[ -f "$manifest" ]] || {
    echo "tap project is not registered: Projects/$formula.json" >&2
    exit 1
  }

  registered_repo="$(ruby -rjson -e 'j=JSON.parse(File.read(ARGV[0])); puts "#{j.dig("formula","repository","owner")}/#{j.dig("formula","repository","name")}"' "$manifest")"
  [[ "$registered_repo" == "$source_repository" ]] || {
    echo "tap registration repository mismatch" >&2
    exit 1
  }

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
    tap_commit="$(git -C "$attempt_dir" log -1 --format=%H -- "Formula/$formula.rb")"
    [[ -n "$tap_commit" ]] || tap_commit="$(git -C "$attempt_dir" rev-parse HEAD)"
    break
  fi

  changed="$(git -C "$attempt_dir" diff --cached --name-only)"
  [[ "$changed" == "Formula/$formula.rb" ]]
  git -C "$attempt_dir" config user.name "GitHub Actions"
  git -C "$attempt_dir" config user.email "actions@github.com"
  git -C "$attempt_dir" commit -m "Update $formula to $version" >/dev/null
  tap_commit="$(git -C "$attempt_dir" rev-parse HEAD)"
  if git -C "$attempt_dir" push origin HEAD:main >/dev/null 2>&1; then
    status="published"
    break
  fi
  tap_commit=""
done

[[ -n "$tap_commit" ]] || {
  echo "tap main moved during all publication attempts" >&2
  exit 1
}

run_id=""
run_url=""
for _ in {1..60}; do
  run_json="$(gh api --method GET "repos/$tap_repository/actions/workflows/tests.yml/runs" \
    -f event=push -f branch=main -f head_sha="$tap_commit" -f per_page=10)"
  count="$(ruby -rjson -e 'puts JSON.parse(STDIN.read).fetch("workflow_runs").length' <<<"$run_json")"
  if [[ "$count" == 1 ]]; then
    IFS=$'\t' read -r run_id run_url <<<"$(ruby -rjson -e 'r=JSON.parse(STDIN.read).fetch("workflow_runs").first; puts [r.fetch("id"),r.fetch("html_url")].join("\t")' <<<"$run_json")"
    break
  fi
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
  abort "tap run failed" unless run.fetch("status") == "completed" && run.fetch("conclusion") == "success"
' "$tap_commit" <<<"$run_state"

if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  {
    echo "homebrew_status=$status"
    echo "tap_commit_sha=$tap_commit"
    echo "tap_run_id=$run_id"
    echo "tap_run_url=$run_url"
  } >>"$GITHUB_OUTPUT"
fi
echo "$status $formula $version: $run_url"
