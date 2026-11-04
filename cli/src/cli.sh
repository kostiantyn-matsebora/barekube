#!/bin/bash

BAREKUBE_RELEASE_URL="https://raw.githubusercontent.com/kostiantyn-matsebora/barekube/master/cli/release/barekube"

# Prints the directory to install into: $HOME/bin when automatic, otherwise asked.
resolve_install_path() {
  local DEFAULT_PATH="$HOME/bin" CHOSEN_PATH
  if [[ "$(is_automatic_mode "$1")" == "0" ]]
  then
    echo "$DEFAULT_PATH"
    return 0
  fi
  CHOSEN_PATH="$(read_input "Enter the path where workspace is installed ($DEFAULT_PATH as default): ")"
  echo "${CHOSEN_PATH:-$DEFAULT_PATH}"
}

upgrade_barekube() {
  local BAREKUBE_PATH
  echo_info "Upgrading barekube CLI $(barekube --version)"
  BAREKUBE_PATH="$(resolve_install_path "$1")"
  echo_message "Upgrading barekube CLI at $BAREKUBE_PATH"
  wget "$BAREKUBE_RELEASE_URL" -O "$BAREKUBE_PATH/barekube"
  chmod +x "$BAREKUBE_PATH/barekube"
  exit_if_error "Error upgrading barekube"
  echo_info "Barekube CLI is upgraded successfully to version $(barekube --version)"
}
