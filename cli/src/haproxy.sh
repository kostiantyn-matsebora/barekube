#!/bin/bash

install_haproxy() {
  run_privileged "Installing HAProxy..." "Error installing HAProxy" \
    apt-get install -y haproxy keepalived
}
