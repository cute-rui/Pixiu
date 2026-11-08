#!/usr/bin/env bash
# Replace the single rolling prerelease for main.
# Tag: prerelease
# Title: Pixiu main prerelease
# The tag is recreated on the packaged commit. This is not a stable release
# and is not marked Latest. Branch history is not rewritten.
set -euo pipefail

assets="${1:?asset directory is required}"
tag="${PIXIU_PRERELEASE_TAG:-prerelease}"
title="${PIXIU_PRERELEASE_TITLE:-Pixiu main prerelease}"
repo="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
sha="${GITHUB_SHA:?GITHUB_SHA is required}"
ref="${GITHUB_REF:-}"
server="${GITHUB_SERVER_URL:-https://github.com}"

if [ "$ref" != "refs/heads/main" ]; then
  echo "Ref ${ref:-unknown} is not main. The rolling prerelease is unchanged."
  exit 0
fi

# Artifact downloads keep each Actions artifact's directory layout.
# Release assets are the files themselves, so flatten and reject collisions.
staged=$(mktemp -d)
trap 'rm -rf "$staged"' EXIT
collect_assets() {
  local path base
  for path in "$1"/*; do
    if [ ! -e "$path" ]; then
      continue
    fi
    if [ -d "$path" ]; then
      collect_assets "$path"
      continue
    fi
    if [ ! -f "$path" ]; then
      continue
    fi
    base=$(basename "$path")
    if [ -e "$staged/$base" ]; then
      echo "duplicate asset name: $base" >&2
      exit 1
    fi
    cp "$path" "$staged/$base"
  done
}
collect_assets "$assets"

files=()
for path in "$staged"/*; do
  if [ -f "$path" ]; then
    files+=("$path")
  fi
done

if [ "${#files[@]}" -eq 0 ]; then
  echo "No installer files in $assets" >&2
  exit 1
fi

missing=0
require_glob() {
  local pattern="$1"
  local found=0
  local path base
  for path in "${files[@]}"; do
    base=$(basename "$path")
    # shellcheck disable=SC2254
    case "$base" in
      $pattern) found=1 ;;
    esac
  done
  if [ "$found" -ne 1 ]; then
    echo "Missing required release asset matching $pattern" >&2
    missing=1
  fi
}

require_glob '*.deb'
require_glob '*.rpm'
require_glob '*.dmg'
require_glob '*setup*.exe'
require_glob 'pixiu-linux-amd64'
require_glob 'pixiu-android.apk'

if [ "$missing" -ne 0 ]; then
  echo "Refusing to publish an incomplete prerelease." >&2
  exit 1
fi

tip=$(gh api "repos/${repo}/commits/main" --jq .sha)
if [ "$tip" != "$sha" ]; then
  echo "main is ${tip}; this run packaged ${sha}. Leaving the rolling prerelease in place."
  exit 0
fi

notes=$(mktemp)
trap 'rm -rf "$staged"; rm -f "$notes"' EXIT
cat >"$notes" <<EOF
Rolling prerelease of main at \`${sha}\`.

${server}/${repo}/commit/${sha}

This is not a stable release. The \`${tag}\` tag points at this commit and replaces the previous main prerelease. It is not marked as Latest.

Desktop installers are unsigned when \`TAURI_SIGNING_PRIVATE_KEY\` is unset. An unsigned Verge build is a successful build.

## Assets
EOF
for path in "${files[@]}"; do
  printf -- '- %s\n' "$(basename "$path")" >>"$notes"
done

if [ "${DRY_RUN:-}" = "1" ]; then
  echo "dry-run: would publish ${tag} at ${sha}"
  printf '%s\n' "${files[@]}"
  cat "$notes"
  exit 0
fi

if gh release view "$tag" --repo "$repo" >/dev/null 2>&1; then
  gh release delete "$tag" --repo "$repo" --yes --cleanup-tag
fi

gh release create "$tag" \
  --repo "$repo" \
  --target "$sha" \
  --title "$title" \
  --prerelease \
  --latest=false \
  --notes-file "$notes" \
  "${files[@]}"

# gh release view --json has no isLatest on the GitHub-hosted CLI.
# /releases/latest is the stable latest and 404s when every release is a prerelease.
meta=$(gh api "repos/${repo}/releases/tags/${tag}" \
  --jq '[.html_url, .prerelease, .draft, .target_commitish] | @tsv')
url=$(printf '%s\n' "$meta" | awk -F '\t' '{print $1}')
is_pre=$(printf '%s\n' "$meta" | awk -F '\t' '{print $2}')
is_draft=$(printf '%s\n' "$meta" | awk -F '\t' '{print $3}')
target_sha=$(printf '%s\n' "$meta" | awk -F '\t' '{print $4}')
latest_tag=""
if latest_tag=$(gh api "repos/${repo}/releases/latest" --jq .tag_name 2>/dev/null); then
  :
else
  latest_tag=""
fi

if [ "$is_pre" != "true" ] || [ "$is_draft" != "false" ] || [ "$target_sha" != "$sha" ] || [ "$latest_tag" = "$tag" ]; then
  echo "Published release is not the rolling main prerelease (${meta} latest=${latest_tag})." >&2
  exit 1
fi

echo "Published ${url}"
