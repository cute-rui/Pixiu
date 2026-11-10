#!/usr/bin/env bash
# Fixture tests for installer staging and the rolling prerelease publisher.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
stage="$root/.github/scripts/stage-installers.sh"
publish="$root/.github/scripts/publish-rolling-prerelease.sh"
fail=0

assert_ok() {
  if ! "$@"; then
    echo "FAIL: $*" >&2
    fail=1
  fi
}

assert_fail() {
  local status=0
  "$@" || status=$?
  if [ "$status" -eq 0 ]; then
    echo "FAIL: expected non-zero: $*" >&2
    fail=1
  fi
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

mkdir -p "$tmp/proj/target/x86_64-unknown-linux-gnu/release/bundle/deb"
mkdir -p "$tmp/proj/target/x86_64-unknown-linux-gnu/release/bundle/dmg"
mkdir -p "$tmp/proj/target/x86_64-unknown-linux-gnu/release/bundle/nsis/nested"
mkdir -p "$tmp/proj/target/x86_64-unknown-linux-gnu/release/bundle/ignore"
printf 'deb' >"$tmp/proj/target/x86_64-unknown-linux-gnu/release/bundle/deb/Pixiu_2.6.0_amd64.deb"
printf 'dmg' >"$tmp/proj/target/x86_64-unknown-linux-gnu/release/bundle/dmg/Pixiu_2.6.0_aarch64.dmg"
printf 'exe' >"$tmp/proj/target/x86_64-unknown-linux-gnu/release/bundle/nsis/nested/Pixiu_2.6.0_x64-setup.exe"
printf 'nope' >"$tmp/proj/target/x86_64-unknown-linux-gnu/release/bundle/ignore/notes.txt"
printf 'lib' >"$tmp/proj/target/x86_64-unknown-linux-gnu/release/bundle/nsis/helper.dll"

out="$tmp/out"
(
  cd "$tmp/proj"
  TARGET=x86_64-unknown-linux-gnu OUT="$out" bash "$stage"
)
assert_ok test -f "$out/Pixiu_2.6.0_amd64.deb"
assert_ok test -f "$out/Pixiu_2.6.0_aarch64.dmg"
assert_ok test -f "$out/Pixiu_2.6.0_x64-setup.exe"
assert_ok test ! -e "$out/notes.txt"
assert_ok test ! -e "$out/helper.dll"

assert_fail bash -c "cd '$tmp/proj' && TARGET=missing OUT='$tmp/missing-out' bash '$stage'"

mkdir -p "$tmp/empty-proj/target/t/release/bundle/empty"
assert_fail bash -c "cd '$tmp/empty-proj' && TARGET=t OUT='$tmp/empty-out' bash '$stage'"

bin="$tmp/bin"
mkdir -p "$bin"
cat >"$bin/gh" <<'EOF'
#!/bin/sh
printf '%s\0' "$@" >> "${GH_ARGS_NUL:?}"
case " $* " in
  *" --json "*)
    printf 'https://github.com/cute-rui/Pixiu/releases/tag/prerelease\ttrue\tfalse\tfalse\t%s\n' "${FAKE_MAIN_SHA:?}"
    exit 0
    ;;
esac
if [ "$1" = "api" ]; then
  case " $* " in
    *"/commits/main"*)
      printf '%s\n' "${FAKE_MAIN_SHA:?}"
      exit 0
      ;;
    *"/releases/latest"*)
      if [ -n "${FAKE_LATEST_TAG:-}" ]; then
        printf '%s\n' "$FAKE_LATEST_TAG"
        exit 0
      fi
      echo "not found" >&2
      exit 1
      ;;
    *"/releases/tags/"*)
      printf 'https://github.com/cute-rui/Pixiu/releases/tag/prerelease\ttrue\tfalse\t%s\n' "${FAKE_MAIN_SHA:?}"
      exit 0
      ;;
  esac
  echo "unexpected api: $*" >&2
  exit 1
fi
if [ "$1" = "release" ] && [ "$2" = "view" ]; then
  if [ "${FAKE_RELEASE_EXISTS:-0}" = "1" ]; then
    exit 0
  fi
  echo "release not found" >&2
  exit 1
fi
if [ "$1" = "release" ] && [ "$2" = "delete" ]; then
  exit 0
fi
if [ "$1" = "release" ] && [ "$2" = "create" ]; then
  exit 0
fi
echo "unexpected gh invocation: $*" >&2
exit 1
EOF
chmod +x "$bin/gh"

has_arg() {
  local want="$1"
  local field
  while IFS= read -r -d '' field || [ -n "$field" ]; do
    if [ "$field" = "$want" ]; then
      return 0
    fi
  done <"$GH_ARGS_NUL"
  return 1
}

has_arg_suffix() {
  local want="$1"
  local field
  while IFS= read -r -d '' field || [ -n "$field" ]; do
    case "$field" in
      *"$want") return 0 ;;
    esac
  done <"$GH_ARGS_NUL"
  return 1
}

run_publish() {
  GH_ARGS_NUL="$tmp/gh-args"
  : >"$GH_ARGS_NUL"
  export GH_ARGS_NUL
  PATH="$bin:$PATH" \
    GITHUB_REPOSITORY="cute-rui/Pixiu" \
    GITHUB_SHA="abc123" \
    GITHUB_REF="refs/heads/main" \
    GITHUB_SERVER_URL="https://github.com" \
    FAKE_MAIN_SHA="${FAKE_MAIN_SHA:-abc123}" \
    FAKE_RELEASE_EXISTS="${FAKE_RELEASE_EXISTS:-0}" \
    FAKE_LATEST_TAG="${FAKE_LATEST_TAG:-}" \
    bash "$publish" "$1"
}

assets="$tmp/assets"
mkdir -p "$assets/pixiu-desktop-linux-amd64"
printf 'd' >"$assets/pixiu-desktop-linux-amd64/Pixiu_2.6.0_amd64.deb"
printf 'r' >"$assets/Pixiu-2.6.0-1.x86_64.rpm"
printf 'm' >"$assets/Pixiu_2.6.0_aarch64.dmg"
printf 'e' >"$assets/Pixiu_2.6.0_x64-setup.exe"
printf 'c' >"$assets/pixiu-linux-amd64"
printf 'w' >"$assets/pixiu-windows-amd64.exe"
printf 'a' >"$assets/pixiu-darwin-arm64"
printf 'k' >"$assets/pixiu-android.apk"

FAKE_RELEASE_EXISTS=1 FAKE_MAIN_SHA=abc123 run_publish "$assets"
assert_ok has_arg "delete"
assert_ok has_arg "--cleanup-tag"
assert_ok has_arg "create"
assert_ok has_arg "--prerelease"
assert_ok has_arg "--latest=false"
assert_ok has_arg "--target"
assert_ok has_arg "abc123"
assert_ok has_arg "prerelease"
assert_ok has_arg "Pixiu main prerelease"
assert_ok has_arg_suffix "/Pixiu_2.6.0_amd64.deb"

FAKE_RELEASE_EXISTS=0 FAKE_MAIN_SHA=abc123 run_publish "$assets"
if has_arg "delete"; then
  echo "FAIL: delete ran when no release existed" >&2
  fail=1
fi

FAKE_MAIN_SHA=newer run_publish "$assets"
if has_arg "delete" || has_arg "create"; then
  echo "FAIL: stale main mutated the release" >&2
  fail=1
fi

GH_ARGS_NUL="$tmp/gh-other"
: >"$GH_ARGS_NUL"
export GH_ARGS_NUL
PATH="$bin:$PATH" \
  GITHUB_REPOSITORY="cute-rui/Pixiu" \
  GITHUB_SHA="abc123" \
  GITHUB_REF="refs/heads/other" \
  FAKE_MAIN_SHA="abc123" \
  bash "$publish" "$assets"
if [ -s "$GH_ARGS_NUL" ]; then
  echo "FAIL: non-main ref invoked gh" >&2
  fail=1
fi

set +e
FAKE_LATEST_TAG=prerelease FAKE_RELEASE_EXISTS=1 FAKE_MAIN_SHA=abc123 run_publish "$assets"
marked_latest=$?
set -e
if [ "$marked_latest" -eq 0 ]; then
  echo "FAIL: a release marked Latest should fail" >&2
  fail=1
fi

rm -f "$assets/Pixiu_2.6.0_aarch64.dmg"
set +e
FAKE_RELEASE_EXISTS=1 FAKE_MAIN_SHA=abc123 run_publish "$assets"
incomplete=$?
set -e
if [ "$incomplete" -eq 0 ]; then
  echo "FAIL: incomplete assets should fail" >&2
  fail=1
fi
if has_arg "delete"; then
  echo "FAIL: incomplete assets deleted the release" >&2
  fail=1
fi

dup="$tmp/dup"
mkdir -p "$dup/a" "$dup/b"
printf '1' >"$dup/a/same.deb"
printf '2' >"$dup/b/same.deb"
set +e
FAKE_MAIN_SHA=abc123 run_publish "$dup"
duplicate=$?
set -e
if [ "$duplicate" -eq 0 ]; then
  echo "FAIL: duplicate asset names should fail" >&2
  fail=1
fi
if has_arg "delete"; then
  echo "FAIL: duplicate asset names deleted the release" >&2
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  echo "installer workflow tests failed" >&2
  exit 1
fi
echo "installer workflow tests passed"
