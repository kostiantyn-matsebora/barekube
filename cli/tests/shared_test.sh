#!/usr/bin/env bash
# Tests for src/shared/shared.sh.
source "$(dirname "${BASH_SOURCE[0]}")/lib/testing.sh"
source "$CLI_DIR/src/shared/shared.sh"

TS_RE='\[[0-9]{4}-[0-9]{2}-[0-9]{2}_[0-9]{2}:[0-9]{2}:[0-9]{2}\]'
ESC=$'\033'

# Helpers that make "$?" non-zero / zero on entry to the function under test.
after_failure() { false; "$@"; echo "continued"; }
after_success() { true; "$@"; echo "continued"; }

# ------------------------------------------------------------------ output

test_timestamp_has_date_and_time() {
  assert_match "^[0-9]{4}-[0-9]{2}-[0-9]{2}_[0-9]{2}:[0-9]{2}:[0-9]{2}\$" "$(timestamp)"
}

test_colour_helpers_use_their_ansi_codes() {
  assert_eq "${ESC}[0;32mx${ESC}[0m" "$(green x)"
  assert_eq "${ESC}[1;33mx${ESC}[0m" "$(yellow_bold x)"
  assert_eq "${ESC}[0;31mx${ESC}[0m" "$(red x)"
  assert_eq "${ESC}[1;92mx${ESC}[0m" "$(green_bold x)"
  assert_eq "${ESC}[0;35mx${ESC}[0m" "$(magenta x)"
}

test_echo_message_is_green_and_timestamped() {
  run echo_message "hello"
  assert_match "^${ESC}\[0;32m${TS_RE} hello" "$output"
}

test_echo_info_is_yellow_and_timestamped() {
  run echo_info "hello"
  assert_match "^${ESC}\[1;33m${TS_RE} hello" "$output"
}

test_echo_important_is_magenta_and_timestamped() {
  run echo_important "hello"
  assert_match "^${ESC}\[0;35m${TS_RE} hello" "$output"
}

test_echo_error_is_red_and_timestamped() {
  run echo_error "hello"
  assert_match "^${ESC}\[0;31m${TS_RE} hello" "$output"
}

# ------------------------------------------------------------------- input

test_question_returns_first_key_only() {
  run_input "yes" question "Continue?"
  assert_eq "y" "$output"
}

test_question_strips_whitespace_answers() {
  run_input " " question "Continue?"
  assert_eq "" "$output"
}

test_question_with_empty_answer_returns_nothing() {
  run_input "" question "Continue?"
  assert_eq "" "$output"
}

test_read_input_returns_the_line_without_whitespace() {
  run_input "  hello big	world " read_input "Say:"
  assert_eq "hellobigworld" "$output"
}

test_read_input_with_empty_line_returns_nothing() {
  run_input "" read_input "Say:"
  assert_eq "" "$output"
}

test_press_any_key_consumes_one_key() {
  run_input "x" press_any_key
  assert_status 0
}

# ----------------------------------------------------------- error handling

test_exit_if_error_is_silent_after_success() {
  run after_success exit_if_error "boom"
  assert_status 0
  assert_eq "continued" "$output"
}

test_exit_if_error_reports_and_stops_after_failure() {
  run after_failure exit_if_error "boom"
  assert_output_contains "boom"
  refute_output_contains "continued"
}

known_bug_exit_if_error_propagates_a_failure_status() {
  # `exit $?` runs after echo_error, so $? is echo_error's 0 - the CLI reports
  # success to the shell after a failed step.
  run after_failure exit_if_error "boom"
  [[ "$status" -ne 0 ]] || fail "exit status was 0"
}

test_echo_final_prints_success_message_after_success() {
  run after_success echo_final "failed msg" "all good"
  assert_status 0
  assert_output_contains "all good"
  refute_output_contains "failed msg"
  assert_output_contains "continued"
}

test_echo_final_prints_error_and_stops_after_failure() {
  run after_failure echo_final "failed msg" "all good"
  assert_output_contains "failed msg"
  refute_output_contains "all good"
  refute_output_contains "continued"
}

known_bug_echo_final_propagates_a_failure_status() {
  run after_failure echo_final "failed msg" "all good"
  [[ "$status" -ne 0 ]] || fail "exit status was 0"
}

# ------------------------------------------------------------------ prompts

test_yes_or_no_skips_the_question_when_second_argument_is_zero() {
  script_fn question n
  run yes_or_no "Sure?" 0
  assert_status 0
  assert_no_calls
}

test_yes_or_no_accepts_lower_and_upper_case_y() {
  local answer
  for answer in y Y; do
    script_fn question "$answer"
    run yes_or_no "Sure?"
    assert_status 0 
  done
}

test_yes_or_no_rejects_everything_else() {
  local answer
  for answer in n N "" x 1; do
    script_fn question "$answer"
    run yes_or_no "Sure?"
    assert_status 1
  done
}

test_yes_or_no_asks_with_a_y_n_suffix() {
  script_fn question y
  yes_or_no "Sure?"
  assert_called "question Sure? [y/n]"
}

test_exit_if_answer_no_continues_after_yes() {
  run after_success exit_if_answer_no
  assert_status 0
  assert_eq "continued" "$output"
}

test_exit_if_answer_no_exits_cleanly_after_no() {
  run after_failure exit_if_answer_no
  assert_status 0
  assert_output_contains "Exiting..."
  refute_output_contains "continued"
}

# ------------------------------------------------------------------- misc

test_is_automatic_mode_recognises_yes_flags() {
  assert_eq 0 "$(is_automatic_mode --yes)"
  assert_eq 0 "$(is_automatic_mode -y)"
}

test_is_automatic_mode_is_interactive_otherwise() {
  assert_eq 1 "$(is_automatic_mode)"
  assert_eq 1 "$(is_automatic_mode --no)"
  assert_eq 1 "$(is_automatic_mode yes)"
}

test_check_if_command_exists_finds_an_existing_command() {
  run check_if_command_exists bash
  assert_status 0
}

test_check_if_command_exists_finds_functions() {
  my_function() { :; }
  run check_if_command_exists my_function
  assert_status 0
}

test_check_if_command_exists_rejects_unknown_command() {
  run check_if_command_exists definitely-not-a-command-xyz
  assert_status 1
}

run_tests
