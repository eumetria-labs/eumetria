#!/bin/sh
# Prints the section of CHANGELOG.md for a release, without its heading:
#
#   scripts/release-notes.sh v1.2.0
#
# The release workflow (.github/workflows/release.yml) uses it for the
# GitHub release's notes and fails when the section is missing or empty.
set -eu
v="${1#v}"
[ -n "$v" ] || { echo "usage: $0 vX.Y.Z" >&2; exit 2; }
notes=$(awk -v v="$v" '
  /^## \[/ { if (on) exit; on = index($0, "## [" v "]") == 1; next }
  /^\[[^]]+\]: / { next }
  on' "$(dirname "$0")/../CHANGELOG.md")
if [ -z "$(printf '%s' "$notes" | tr -d '[:space:]')" ]; then
  echo "CHANGELOG.md has no section \"## [$v]\" (or it is empty)" >&2
  exit 1
fi
printf '%s\n' "$notes" | sed -e '/./,$!d'
