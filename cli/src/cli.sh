#!/bin/bash
#!/bin/bash

upgrade_barekube() {
  AUTOMATIC_MODE=$(is_automatic_mode "$1")
  
  echo_info "Upgrading barekube CLI $(barekube --version)"
  local BAREKUBE_HOME_PATH="$HOME/bin"
  local BAREKUBE_PATH="$BAREKUBE_HOME_PATH"
  if [[ $AUTOMATIC_MODE != "0" ]]
  then
    BAREKUBE_PATH="$(question "Enter the path where workspace is installed ($BAREKUBE_HOME_PATH as default): ")"
    if [ -z "$BAREKUBE_PATH" ]
    then
        BAREKUBE_PATH="$BAREKUBE_HOME_PATH"
    fi
  fi
  echo_message "Upgrading barekube CLI at $BAREKUBE_PATH"
  wget https://raw.githubusercontent.com/kostiantyn-matsebora/barekube/master/cli/release/barekube -O "$BAREKUBE_PATH/barekube"
  chmod +x "$BAREKUBE_PATH/barekube"
  exit_if_error "Error upgrading barekube"
  echo_info "Barekube CLI is upgraded successfully to version $(barekube --version)"
}
