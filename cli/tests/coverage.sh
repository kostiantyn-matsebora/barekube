#!/usr/bin/env bash
# Line-coverage report from an xtrace log produced by `run.sh --coverage`.
# Usage: coverage.sh TRACE_FILE [-v]
# "Executable" lines are approximated: comments, blank lines, bare block
# keywords, function headers, heredoc bodies and continuation lines are ignored.
set -u

TESTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLI_DIR="$(cd "$TESTS_DIR/.." && pwd)"
trace="$1"
verbose="${2:-}"

# Normalise executed lines to "relative/path:line" (wrapped command snippets
# live under .../wrap/commands/...).
hits="$(mktemp)"
awk -F'|' -v root="$CLI_DIR/" '
  /^\++COV\|/ {
    path = $2
    sub(/^.*\/wrap\//, "", path)
    sub("^" root, "", path)
    print path ":" $3
  }' "$trace" | sort -u >"$hits"

executable_lines() {
  awk '
    BEGIN { heredoc = ""; continued = 0 }
    {
      line = $0
      if (heredoc != "") {
        if (line == heredoc) heredoc = ""
        next
      }
      t = line; gsub(/^[ \t]+|[ \t]+$/, "", t)
      was_continued = continued
      continued = (t ~ /\\$/)
      if (was_continued) next
      if (t == "" || t ~ /^#/) next
      if (t ~ /^(then|else|fi|do|done|esac|\{|\}|;;|\))$/) next
      if (t ~ /^(function +)?[A-Za-z_][A-Za-z0-9_-]*[ ]*\(\)[ ]*\{?$/) next
      if (match(t, /<<-?[ ]*["\047]?[A-Za-z_]+/)) {
        tag = substr(t, RSTART, RLENGTH); sub(/^<<-?[ ]*["\047]?/, "", tag)
        heredoc = tag
      }
      print NR
    }' "$1"
}

printf '\nCoverage (executable lines executed by the unit tests)\n'
printf '%-45s %6s %6s %7s\n' FILE LINES HIT COVER
all=0 covered=0
while IFS= read -r file; do
  rel="${file#"$CLI_DIR"/}"
  total=0 hit=0 missed=()
  while IFS= read -r n; do
    total=$((total + 1))
    if grep -Fxq "$rel:$n" "$hits"; then hit=$((hit + 1)); else missed+=("$n"); fi
  done < <(executable_lines "$file")
  ((total)) || continue
  all=$((all + total)); covered=$((covered + hit))
  printf '%-45s %6d %6d %6d%%\n' "$rel" "$total" "$hit" $((hit * 100 / total))
  if [[ "$verbose" == "-v" && ${#missed[@]} -gt 0 ]]; then
    for n in "${missed[@]}"; do printf '    %s:%s: %s\n' "$rel" "$n" "$(sed -n "${n}p" "$file")"; done
  fi
done < <(find "$CLI_DIR/src" "$CLI_DIR/commands" -name '*.sh' | sort)
printf '%-45s %6d %6d %6d%%\n' TOTAL "$all" "$covered" $((all ? covered * 100 / all : 0))
rm -f "$hits"
