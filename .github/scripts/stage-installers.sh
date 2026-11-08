#!/usr/bin/env bash
# Copy Tauri installer files from target/<triple>/release/bundle into $OUT.
# Bash 3.2 compatible (macOS runners).
set -euo pipefail

target="${TARGET:?TARGET is required}"
out="${OUT:?OUT is required}"
bundle="target/${target}/release/bundle"

if [ ! -d "$bundle" ]; then
  echo "bundle directory not found: $bundle" >&2
  exit 1
fi

mkdir -p "$out"
copied=0

walk() {
  local path
  for path in "$1"/*; do
    if [ ! -e "$path" ]; then
      continue
    fi
    if [ -d "$path" ]; then
      walk "$path"
      continue
    fi
    case "$path" in
      *.deb | *.rpm | *.dmg | *.exe | *.msi | *.AppImage | *.sig)
        cp "$path" "$out/"
        copied=$((copied + 1))
        echo "staged $(basename "$path")"
        ;;
      *) ;;
    esac
  done
}

walk "$bundle"

if [ "$copied" -eq 0 ]; then
  echo "no installers under $bundle" >&2
  exit 1
fi
