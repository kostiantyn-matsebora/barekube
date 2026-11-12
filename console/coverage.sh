#!/usr/bin/env bash
# Runs the console tests with the race detector and fails when any package under
# internal/ has less than MIN percent statement coverage (85 by default).
# cmd/ only wires things together and is not counted.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
min="${1:-85}"
out="$(GOTOOLCHAIN=auto go test -race -cover ./internal/... 2>&1)" || { printf '%s\n' "$out"; exit 1; }
printf '%s\n' "$out"
low="$(awk -v min="$min" '/coverage:/ { gsub("%", "", $5); if ($5 + 0 < min) print $2 " " $5 "%" }' <<<"$out")"
if [[ -n "$low" ]]; then
  echo "Below $min% statement coverage:" >&2
  echo "$low" >&2
  exit 1
fi
