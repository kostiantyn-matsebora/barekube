#!/bin/bash

install_haproxy() {
  echo_message "Installing HAProxy..."
  # Install HAProxy
  sudo apt-get install -y haproxy keepalived
  exit_if_error "Error installing HAProxy"
}
