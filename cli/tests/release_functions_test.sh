#!/usr/bin/env bash
# Tests of the bashly runtime helpers embedded in release/barekube, loaded
# in-process (the script only runs `main` when executed, not when sourced),
# plus a check that the release bundle matches the sources it was built from.
source "$(dirname "${BASH_SOURCE[0]}")/lib/testing.sh"
source "$CLI_DIR/release/barekube"

# The release defines its own `run`, so the framework's helper is shadowed.
capture() { output=$("$@" 2>&1); status=$?; }

setup() {
  declare -ga input=()
  declare -gA args=() deps=()
  declare -ga env_var_names=()
}

# ------------------------------------------------------ normalize_input

test_normalize_input_keeps_plain_arguments() {
  normalize_input install dependencies
  assert_eq "install dependencies" "${input[*]}"
}

test_normalize_input_splits_long_flag_with_value() {
  normalize_input --flag=value
  assert_eq "--flag|value" "$(IFS='|'; echo "${input[*]}")"
}

test_normalize_input_splits_short_flag_with_value() {
  normalize_input -f=value
  assert_eq "-f|value" "$(IFS='|'; echo "${input[*]}")"
}

test_normalize_input_expands_compact_short_flags() {
  normalize_input -abc
  assert_eq "-a -b -c" "${input[*]}"
}

test_normalize_input_leaves_single_short_flag_alone() {
  normalize_input -y
  assert_eq "-y" "${input[*]}"
}

test_normalize_input_passes_everything_after_double_dash_through() {
  normalize_input -- -abc --flag=value
  assert_eq "--|-abc|--flag=value" "$(IFS='|'; echo "${input[*]}")"
}

# --------------------------------------------------------- inspect_args

test_inspect_args_reports_no_args() {
  capture inspect_args
  assert_eq "args: none" "$output"
}

test_inspect_args_lists_sorted_args() {
  args[--yes]=1
  args[--reinstall]=1
  capture inspect_args
  assert_eq $'args:\n- ${args[--reinstall]} = 1\n- ${args[--yes]} = 1' "$output"
}

test_inspect_args_lists_deps_and_environment_variables() {
  deps[wget]=/usr/bin/wget
  args[--yes]=1
  env_var_names=(B_VAR A_VAR)
  A_VAR=a
  capture inspect_args
  assert_output_contains '- ${deps[wget]} = /usr/bin/wget'
  assert_output_contains $'environment variables:\n- $A_VAR = a\n- $B_VAR = '
}

test_version_command_prints_the_version() {
  version=1.2.3
  capture version_command
  assert_eq "1.2.3" "$output"
}

# ------------------------------------------------------- bundle in sync

# Every non-empty source line must appear verbatim in the generated release;
# a miss means the release was not rebuilt (`bashly generate`) after an edit.
check_bundled() {
  local file="$1" line missing=() bundle
  bundle="$(<"$CLI_DIR/release/barekube")"
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line%"${line##*[![:space:]]}"}"
    [[ -z "$line" || "$line" == '#'* ]] && continue # bashly rewrites comments
    [[ "$bundle" == *"$line"* ]] || missing+=("$line")
  done <"$file"
  ((${#missing[@]} == 0)) || fail "${file#"$CLI_DIR"/} not in release; first missing: [${missing[0]}]"
}

test_release_bundles_every_library_script() {
  local file
  for file in "$CLI_DIR"/src/*.sh "$CLI_DIR"/src/shared/*.sh; do
    check_bundled "$file"
  done
}

test_release_bundles_every_command_script() {
  local file
  for file in "$CLI_DIR"/commands/*.sh "$CLI_DIR"/commands/*/*.sh; do
    check_bundled "$file"
  done
}

test_release_declares_every_command_from_bashly_config() {
  local name bundle
  bundle="$(<"$CLI_DIR/release/barekube")"
  while IFS= read -r name; do
    [[ "$bundle" == *"$name"* ]] || fail "command [$name] missing in release"
  done < <(sed -n 's/^ *- name: *//p' "$CLI_DIR/bashly.yml" | tr -d '\r')
}

run_tests
