#!/usr/bin/env bash
# Tests for src/shared/colors.sh (bashly standard colour library).
source "$(dirname "${BASH_SOURCE[0]}")/lib/testing.sh"
source "$CLI_DIR/src/shared/colors.sh"

setup() { unset NO_COLOR; }

declare -A CODES=(
  [red]='31' [green]='32' [yellow]='33' [blue]='34' [magenta]='35' [cyan]='36'
  [bold]='1' [underlined]='4'
  [red_bold]='1;31' [green_bold]='1;32' [yellow_bold]='1;33' [blue_bold]='1;34'
  [magenta_bold]='1;35' [cyan_bold]='1;36'
  [red_underlined]='4;31' [green_underlined]='4;32' [yellow_underlined]='4;33'
  [blue_underlined]='4;34' [magenta_underlined]='4;35' [cyan_underlined]='4;36'
)

test_every_colour_wraps_text_in_its_escape_code() {
  local fn
  for fn in "${!CODES[@]}"; do
    assert_eq $'\e['"${CODES[$fn]}"$'mhello\e[0m' "$("$fn" hello)" "$fn"
  done
}

test_arguments_are_joined_with_spaces() {
  assert_eq $'\e[31ma b c\e[0m' "$(red a b c)"
}

test_backslash_escapes_are_interpreted() {
  assert_eq $'\e[32ma\tb\e[0m' "$(green 'a\tb')"
}

test_no_color_disables_formatting() {
  local fn
  export NO_COLOR=1
  for fn in "${!CODES[@]}"; do
    assert_eq "hello" "$("$fn" hello)" "$fn"
  done
}

test_empty_no_color_still_disables_formatting() {
  export NO_COLOR=
  assert_eq "hello" "$(red hello)"
}

test_print_in_color_uses_the_given_code() {
  assert_eq $'\e[7mx\e[0m' "$(print_in_color $'\e[7m' x)"
}

run_tests
