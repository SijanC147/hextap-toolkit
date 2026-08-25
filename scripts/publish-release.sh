#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "usage: publish-release.sh <owner/repository> <tag> <asset-directory> <true|false-prerelease>" >&2
  exit 64
fi

repository="$1"
tag="$2"
asset_dir="$(cd -- "$3" && pwd)"
prerelease="$4"

if [[ ! "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  echo "invalid release repository" >&2
  exit 64
fi
if [[ ! "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "invalid release tag" >&2
  exit 64
fi
if [[ "$prerelease" != true && "$prerelease" != false ]]; then
  echo "prerelease must be true or false" >&2
  exit 64
fi

mapfile -t expected_assets < <(find "$asset_dir" -mindepth 1 -maxdepth 1 -type f -print | sed 's#.*/##' | LC_ALL=C sort)
if [[ ${#expected_assets[@]} -lt 3 ]] || ! printf '%s\n' "${expected_assets[@]}" | grep -Fxq SHA256SUMS; then
  echo "release asset directory is incomplete" >&2
  exit 1
fi

release_exists=false
if gh release view "$tag" --repo "$repository" >/dev/null 2>&1; then
  release_exists=true
fi

if [[ "$release_exists" == true ]]; then
  state="$(gh release view "$tag" --repo "$repository" --json isDraft,isPrerelease --jq '[.isDraft,.isPrerelease] | @tsv')"
  IFS=$'\t' read -r is_draft is_prerelease <<<"$state"
  if [[ "$is_draft" != true ]]; then
    echo "release $tag is already published; refusing to mutate it" >&2
    exit 1
  fi
  if [[ "$is_prerelease" != "$prerelease" ]]; then
    echo "existing draft prerelease state does not match" >&2
    exit 1
  fi
else
  args=("$tag" --repo "$repository" --verify-tag --draft --generate-notes)
  [[ "$prerelease" != true ]] || args+=(--prerelease)
  gh release create "${args[@]}"
fi

comparison_dir="$(mktemp -d)"
trap 'find "$comparison_dir" -depth -delete' EXIT
existing="$(gh release view "$tag" --repo "$repository" --json assets --jq '.assets[].name')"

for name in "${expected_assets[@]}"; do
  if grep -Fxq "$name" <<<"$existing"; then
    mkdir -p "$comparison_dir/$name"
    gh release download "$tag" --repo "$repository" --pattern "$name" --dir "$comparison_dir/$name"
    cmp "$asset_dir/$name" "$comparison_dir/$name/$name" || {
      echo "existing draft asset differs: $name" >&2
      exit 1
    }
  else
    gh release upload "$tag" "$asset_dir/$name" --repo "$repository"
  fi
done

actual="$(gh release view "$tag" --repo "$repository" --json assets --jq '.assets[].name' | LC_ALL=C sort)"
expected="$(printf '%s\n' "${expected_assets[@]}" | LC_ALL=C sort)"
if [[ "$actual" != "$expected" ]]; then
  echo "release asset set mismatch" >&2
  exit 1
fi

gh release edit "$tag" --repo "$repository" --draft=false
echo "published $repository release $tag"
