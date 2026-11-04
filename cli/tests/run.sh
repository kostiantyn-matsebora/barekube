#!/usr/bin/env bash
# Runs the barekube CLI unit tests.
#
#   tests/run.sh                 run every tests/*_test.sh
#   tests/run.sh FILE...         run selected test files
#   tests/run.sh --coverage      also print line coverage of src/ and commands/
#   tests/run.sh --coverage -v   ... and list the uncovered lines
set -u

TESTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
coverage=0
verbose=()
files=()
for arg in "$@"; do
  case "$arg" in
    --coverage) coverage=1 ;;
    -v) verbose=(-v) ;;
    *) files+=("$arg") ;;
  esac
done
((${#files[@]})) || files=("$TESTS_DIR"/*_test.sh)

if ((coverage)); then
  COVERAGE_FILE="$(mktemp)"
  export COVERAGE_FILE
fi

total_pass=0 total_fail=0 total_known=0 exit_code=0
for file in "${files[@]}"; do
  printf '%s\n' "$(basename "$file")"
  out="$(bash "$file" </dev/null)"
  rc=$?
  printf '%s\n' "$out" | grep -v '^RESULT '
  read -r _ pass fail known < <(printf '%s\n' "$out" | grep '^RESULT ' || echo 'RESULT 0 1 0')
  total_pass=$((total_pass + pass))
  total_fail=$((total_fail + fail))
  total_known=$((total_known + known))
  ((rc == 0)) || exit_code=1
done

printf '\n%d passed, %d failed, %d known bugs (xfail)\n' "$total_pass" "$total_fail" "$total_known"

if ((coverage)); then
  "$TESTS_DIR/coverage.sh" "$COVERAGE_FILE" "${verbose[@]}"
  rm -f "$COVERAGE_FILE"
fi
exit "$exit_code"
