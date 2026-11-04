#!/bin/bash

BAREKUBE_RELEASE_URL="https://raw.githubusercontent.com/kostiantyn-matsebora/barekube/master/cli/release/barekube"

upgrade_barekube() {
  local BAREKUBE_HOME_PATH="$HOME/bin"
  local BAREKUBE_PATH="$BAREKUBE_HOME_PATH"

  echo_info "Upgrading barekube CLI $(barekube --version)"
  if [[ "$(is_automatic_mode "$1")" != "0" ]]
  then
    BAREKUBE_PATH="$(read_input "Enter the path where workspace is installed ($BAREKUBE_HOME_PATH as default): ")"
    BAREKUBE_PATH="${BAREKUBE_PATH:-$BAREKUBE_HOME_PATH}"
  fi
  echo_message "Upgrading barekube CLI at $BAREKUBE_PATH"
  wget "$BAREKUBE_RELEASE_URL" -O "$BAREKUBE_PATH/barekube"
  chmod +x "$BAREKUBE_PATH/barekube"
  exit_if_error "Error upgrading barekube"
  echo_info "Barekube CLI is upgraded successfully to version $(barekube --version)"
}
