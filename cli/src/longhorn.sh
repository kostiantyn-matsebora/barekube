#!/bin/bash
install_longhorn_dependencies() {
    echo_message "Updating packages..." && sudo apt update
    echo_message "Installing open-iscsi..." && sudo apt install -y open-iscsi
    echo_message "Installing bash..." && sudo apt install -y bash
    echo_message "Installing curl..." && sudo apt install -y curl
    echo_message "Installing grep..." && sudo apt install -y grep
    echo_message "Installing nfs-common..." && sudo apt install -y nfs-common
    return 0
}
