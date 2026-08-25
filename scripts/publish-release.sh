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

comparison_dir="$(mktemp -d)"
trap 'find "$comparison_dir" -depth -delete' EXIT
expected="$(printf '%s\n' "${expected_assets[@]}" | LC_ALL=C sort)"

if [[ "$release_exists" == true ]]; then
  state="$(gh release view "$tag" --repo "$repository" --json isDraft,isImmutable,isPrerelease --jq '[.isDraft,.isImmutable,.isPrerelease] | @tsv')"
  IFS=$'\t' read -r is_draft is_immutable is_prerelease <<<"$state"
  if [[ "$is_draft" != true && "$is_draft" != false ]] ||
    [[ "$is_immutable" != true && "$is_immutable" != false ]] ||
    [[ "$is_prerelease" != true && "$is_prerelease" != false ]]; then
    echo "existing release state is invalid" >&2
    exit 1
  fi
  if [[ "$is_prerelease" != "$prerelease" ]]; then
    if [[ "$is_draft" == true ]]; then
      echo "existing draft prerelease state does not match" >&2
    else
      echo "published release prerelease state does not match" >&2
    fi
    exit 1
  fi
  if [[ "$is_draft" != true ]]; then
    if [[ "$is_immutable" != true ]]; then
      echo "published release is not immutable" >&2
      exit 1
    fi
    actual="$(gh release view "$tag" --repo "$repository" --json assets --jq '.assets[].name' | LC_ALL=C sort)"
    if [[ "$actual" != "$expected" ]]; then
      echo "published release asset set mismatch" >&2
      exit 1
    fi
    for name in "${expected_assets[@]}"; do
      mkdir -p "$comparison_dir/$name"
      gh release download "$tag" --repo "$repository" --pattern "$name" --dir "$comparison_dir/$name"
      downloaded_asset="$comparison_dir/$name/$name"
      if [[ ! -f "$downloaded_asset" || -L "$downloaded_asset" ]]; then
        echo "published release asset is not a regular file: $name" >&2
        exit 1
      fi
      cmp "$asset_dir/$name" "$downloaded_asset" || {
        echo "published release asset differs: $name" >&2
        exit 1
      }
    done
    gh release verify "$tag" --repo "$repository"
    echo "verified immutable $repository release $tag"
    exit 0
  fi
else
  args=("$tag" --repo "$repository" --verify-tag --draft --generate-notes)
  [[ "$prerelease" != true ]] || args+=(--prerelease)
  gh release create "${args[@]}"
fi

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
if [[ "$actual" != "$expected" ]]; then
  echo "release asset set mismatch" >&2
  exit 1
fi

gh release edit "$tag" --repo "$repository" --draft=false
echo "published $repository release $tag"
