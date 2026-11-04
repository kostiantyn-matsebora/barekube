#!/usr/bin/env bash
# Minimal, dependency-free test framework for the barekube CLI scripts.
#
# A test file sources this library, sources the script(s) under test, defines
# functions and ends with `run_tests`:
#   test_*        must pass (every assertion holds)
#   known_bug_*   documents a defect: expected to FAIL today. When the defect is
#                 fixed the test starts passing and the run reports it as an
#                 error, prompting a rename to test_*.
#   setup         optional, runs before every test (inside the test's subshell)
#
# Every test runs in its own subshell with a private $TEST_TMP, so mocks,
# variables and `exit` calls made by the code under test never leak.

TESTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI_DIR="$(cd "$TESTS_DIR/.." && pwd)"

if [[ -n "${COVERAGE_FILE:-}" ]]; then
  # Line coverage via xtrace: every executed line is appended to $COVERAGE_FILE.
  exec 9>>"$COVERAGE_FILE"
  BASH_XTRACEFD=9
  PS4='+COV|${BASH_SOURCE[0]}|${LINENO}|'
  set -x
fi

# ---------------------------------------------------------------- assertions

fail() {
  printf '    %s\n' "$*" >&2
  exit 1
}

assert_eq() {
  [[ "$1" == "$2" ]] || fail "expected: [$1]"$'\n    '"actual:   [$2]${3:+ ($3)}"
}

assert_status() { assert_eq "$1" "$status" "exit status; output: $output"; }

assert_contains() {
  [[ "$1" == *"$2"* ]] || fail "expected [$1] to contain [$2]"
}

refute_contains() {
  [[ "$1" != *"$2"* ]] || fail "expected [$1] NOT to contain [$2]"
}

assert_output_contains() { assert_contains "$output" "$1"; }
refute_output_contains() { refute_contains "$output" "$1"; }

assert_match() {
  [[ "$2" =~ $1 ]] || fail "expected [$2] to match /$1/"
}

assert_file_contains() {
  [[ -f "$1" ]] || fail "file $1 does not exist"
  assert_contains "$(<"$1")" "$2"
}

# ----------------------------------------------------------------- execution

# run CMD...: executes in a subshell so `exit` is survivable; fills $output
# (stdout+stderr) and $status.
run() {
  output=$("$@" 2>&1)
  status=$?
}

# run_input TEXT CMD...: like run, with TEXT on stdin.
run_input() {
  local input="$1"
  shift
  output=$("$@" 2>&1 <<<"$input")
  status=$?
}

# Removes ANSI colour sequences from stdin.
strip_ansi() { sed $'s/\x1b\\[[0-9;]*m//g'; }

# ---------------------------------------------------------------------- mocks
# Mocks log "name arg..." lines to $CALLS (a file, so calls made in subshells
# and pipelines are still recorded).

_mock_key() { printf '%s' "${1//[^a-zA-Z0-9_]/_}"; }

_mock_call() {
  local name="$1" key out_var fail_var stdin_var
  shift
  key="$(_mock_key "$name")"
  out_var="MOCK_OUT_$key"
  fail_var="MOCK_FAIL_$key"
  stdin_var="MOCK_STDIN_$key"
  printf '%s\n' "$name $*" >>"$CALLS"
  [[ -n "${!stdin_var:-}" ]] && cat >>"$STDIN_LOG"
  [[ -n "${!out_var:-}" ]] && printf '%s\n' "${!out_var}"
  # shellcheck disable=SC2053
  if [[ -n "${!fail_var:-}" && "$*" == ${!fail_var} ]]; then
    return 1
  fi
  return 0
}

mock() {
  local name
  for name in "$@"; do
    eval "$name() { _mock_call '$name' \"\$@\"; }"
  done
}

# mock_fail NAME [GLOB]: the mock returns 1 when its arguments match GLOB.
mock_fail() { printf -v "MOCK_FAIL_$(_mock_key "$1")" '%s' "${2:-*}"; }
mock_output() { printf -v "MOCK_OUT_$(_mock_key "$1")" '%s' "$2"; }
mock_capture_stdin() { printf -v "MOCK_STDIN_$(_mock_key "$1")" '%s' 1; }

queue_set() {
  local name="$1"
  shift
  : >"$TEST_TMP/queue_$name"
  (($#)) && printf '%s\n' "$@" >"$TEST_TMP/queue_$name"
  return 0
}

queue_pop() {
  local file="$TEST_TMP/queue_$1" line
  IFS= read -r line <"$file" || return 1
  tail -n +2 "$file" >"$file.next"
  mv "$file.next" "$file"
  printf '%s\n' "$line"
}

# script_fn NAME VALUE...: NAME becomes a function answering with the given
# values in order (one per call) - used for `question` and `read_input`.
script_fn() {
  local name="$1"
  shift
  queue_set "$name" "$@"
  eval "$name() {
    printf '%s\n' \"$name \$*\" >>\"\$CALLS\"
    queue_pop '$name' || { printf '%s\n' '!! $name: no scripted answer left' >>\"\$CALLS\"; echo; }
  }"
}

calls() { cat "$CALLS"; }

# assert_calls LINE...: the recorded calls are exactly LINEs, in order.
assert_calls() {
  local expected actual
  expected="$(printf '%s\n' "$@")"
  actual="$(calls)"
  assert_eq "$expected" "$actual" "recorded calls"
}

# Pure-bash line matching: tests may shadow grep with a mock or a PATH stub.
_has_call() {
  local line
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ "$line" == "$1" ]] && return 0
  done <"$CALLS"
  return 1
}

_has_call_matching() {
  local line
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ "$line" =~ $1 ]] && return 0
  done <"$CALLS"
  return 1
}

assert_called() {
  _has_call "$1" || fail "expected call [$1]; recorded:"$'\n'"$(calls)"
}

# assert_calls_in_order LINE...: LINEs appear in the call log in this order
# (other calls may be interleaved).
assert_calls_in_order() {
  local want index=1
  local -a recorded
  mapfile -t recorded <"$CALLS"
  for want in "$@"; do
    while ((index <= ${#recorded[@]})) && [[ "${recorded[index - 1]}" != "$want" ]]; do index=$((index + 1)); done
    ((index <= ${#recorded[@]})) || fail "call [$want] missing or out of order; recorded:"$'\n'"$(calls)"
    index=$((index + 1))
  done
}

assert_called_matching() {
  _has_call_matching "$1" || fail "expected a call matching [$1]; recorded:"$'\n'"$(calls)"
}

refute_called() {
  ! _has_call "$1" || fail "unexpected call [$1]; recorded:"$'\n'"$(calls)"
}

refute_called_matching() {
  ! _has_call_matching "$1" || fail "unexpected call matching [$1]; recorded:"$'\n'"$(calls)"
}

assert_no_calls() { assert_eq "" "$(calls)" "recorded calls"; }

# ------------------------------------------------------------ command loader

WRAP_DIR=''

# load_command RELATIVE_PATH FUNCTION: wraps a bashly command snippet
# (commands/<path>.sh) into FUNCTION exactly as bashly does. The wrapper keeps
# the original line numbers, so coverage maps back onto the real file.
load_command() {
  local relative="commands/$1.sh" wrapper
  [[ -n "$WRAP_DIR" ]] || { WRAP_DIR="$(mktemp -d)/wrap"; mkdir -p "$WRAP_DIR"; }
  wrapper="$WRAP_DIR/$relative"
  mkdir -p "$(dirname "$wrapper")"
  { printf '%s() { ' "$2"; cat "$CLI_DIR/$relative"; printf '\n}\n'; } >"$wrapper"
  # shellcheck disable=SC1090
  source "$wrapper"
}

# ---------------------------------------------------------------- the runner

run_tests() {
  local name passed=0 failed=0 known=0 log rc
  local -a names
  mapfile -t names < <(declare -F | awk '{print $3}' | grep -E '^(test_|known_bug_)' | sort)
  for name in "${names[@]}"; do
    log="$(mktemp)"
    (
      TEST_TMP="$(mktemp -d)"
      trap 'rm -rf "$TEST_TMP"' EXIT
      CALLS="$TEST_TMP/calls"
      STDIN_LOG="$TEST_TMP/stdin"
      : >"$CALLS" >"$STDIN_LOG"
      if declare -F setup >/dev/null; then setup; fi
      "$name"
    ) >"$log" 2>&1 </dev/null
    rc=$?
    case "$name:$rc" in
      known_bug_*:0)
        printf '  ERROR  %s passes now - rename it to test_*\n' "$name"
        failed=$((failed + 1))
        ;;
      known_bug_*:*)
        printf '  XFAIL  %s\n' "$name"
        known=$((known + 1))
        ;;
      *:0) passed=$((passed + 1)) ;;
      *)
        printf '  FAIL   %s\n' "$name"
        sed 's/^/         /' "$log"
        failed=$((failed + 1))
        ;;
    esac
    rm -f "$log"
  done
  [[ -z "$WRAP_DIR" ]] || rm -rf "$(dirname "$WRAP_DIR")"
  printf 'RESULT %s %s %s\n' "$passed" "$failed" "$known"
  ((failed == 0))
}
