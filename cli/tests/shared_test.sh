#!/usr/bin/env bash
# Tests for src/shared/shared.sh.
source "$(dirname "${BASH_SOURCE[0]}")/lib/testing.sh"
source "$CLI_DIR/src/shared/colors.sh"
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


test_echo_message_is_green_and_timestamped() {
  run echo_message "hello"
  assert_match "^${ESC}\[32m${TS_RE} hello" "$output"
}

test_echo_info_is_yellow_and_timestamped() {
  run echo_info "hello"
  assert_match "^${ESC}\[1;33m${TS_RE} hello" "$output"
}


test_echo_error_is_red_and_timestamped() {
  run echo_error "hello"
  assert_match "^${ESC}\[31m${TS_RE} hello" "$output"
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


test_prompt_message_is_timestamped_bold_green() {
  run prompt_message "Hi"
  assert_match "^${ESC}\[1;32m${TS_RE} Hi" "$output"
}

test_remove_whitespace_strips_spaces_tabs_and_newlines() {
  assert_eq "ab" "$(remove_whitespace $' a\tb\n ')"
}

test_read_required_input_returns_the_value() {
  script_fn read_input value
  run read_required_input "Prompt:" "is required"
  assert_status 0
  assert_eq "value" "$output"
}

test_read_required_input_fails_with_the_message_when_empty() {
  script_fn read_input ""
  run read_required_input "Prompt:" "is required"
  assert_status 1
  assert_output_contains "is required"
}

test_read_required_input_reports_errors_on_stderr() {
  script_fn read_input ""
  assert_eq "" "$(read_required_input "Prompt:" "is required" 2>/dev/null)"
}

# run_privileged is exercised with a mocked sudo
test_run_privileged_announces_and_runs_the_command_with_sudo() {
  mock sudo
  run run_privileged "Doing it..." "Could not" apt install -y pkg
  assert_status 0
  assert_output_contains "Doing it..."
  assert_calls "sudo apt install -y pkg"
}

test_run_privileged_reports_failure_and_stops() {
  mock sudo
  mock_fail sudo
  run_privileged_then_continue() { run_privileged "Doing it..." "Could not" apt update; echo continued; }
  run run_privileged_then_continue
  assert_status 1
  assert_output_contains "Could not"
  refute_output_contains "continued"
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

test_exit_if_error_propagates_a_failure_status() {
  # Regression: `exit $?` ran after echo_error, so a failed step exited 0.
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

test_echo_final_propagates_a_failure_status() {
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
