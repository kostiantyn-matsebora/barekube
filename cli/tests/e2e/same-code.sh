#!/usr/bin/env bash
# same-code.sh OLD NEW: exits 0 when the two commits have the same code as far as
# the cluster test is concerned, so a green run on OLD also covers NEW.
# Documentation (*.md) and version numbers do not count; everything the test
# builds or runs does.
set -euo pipefail

old="$1" new="$2"

paths=(
  cli/src cli/commands cli/settings.yml cli/bashly.yml cli/release cli/tests/e2e
  orchestration console .github/workflows/e2e.yml
)

# A version bump changes these lines and nothing that runs differently.
without_versions() {
  grep -vE '^[[:space:]]*(declare -g )?version[:=]|^[[:space:]]*declare -g version=|^[[:space:]]*barekube_version:' || true
}

status=0
while IFS= read -r file; do
  case "$file" in *.md) continue ;; esac
  a="$(git show "$old:$file" 2>/dev/null | without_versions || true)"
  b="$(git show "$new:$file" 2>/dev/null | without_versions || true)"
  if [[ "$a" != "$b" ]]; then
    echo "differs: $file"
    status=1
  fi
done < <(git diff --name-only "$old" "$new" -- "${paths[@]}")
exit "$status"
