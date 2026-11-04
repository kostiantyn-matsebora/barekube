#!/bin/bash
LONGHORN_PACKAGES=(open-iscsi bash curl grep nfs-common)

install_longhorn_dependencies() {
    local PACKAGE
    echo_message "Updating packages..." && sudo apt update || return 1
    for PACKAGE in "${LONGHORN_PACKAGES[@]}"
    do
        echo_message "Installing $PACKAGE..." && sudo apt install -y "$PACKAGE" || return 1
    done
    return 0
}
