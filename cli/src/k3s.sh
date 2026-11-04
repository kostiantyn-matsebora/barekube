#!/bin/bash

K3S_INSTALL_URL="https://get.k3s.io"
K3S_FLANNEL_BACKEND="wireguard-native"

check_k3s_installed() {
    echo_message "Checking if k3s is already installed..."
    if check_if_command_exists "k3s"
    then
        echo_info "k3s is already installed."
        if [[ "$1" -ne "1" ]]
        then
            exit 0
        fi
    fi
}

gather_additional_args() {
    local ADDITIONAL_ARGS=""
    yes_or_no "Do you want to set additional parameters?" || return 0
    if yes_or_no "Do you want to disable load balancer"
    then
        ADDITIONAL_ARGS="$ADDITIONAL_ARGS --disable=servicelb"
    fi
    if yes_or_no "Do you want to disable traefik"
    then
        ADDITIONAL_ARGS="$ADDITIONAL_ARGS --disable=traefik"
    fi
    if yes_or_no "Do you want to set TLS SAN?"
    then
        ADDITIONAL_ARGS="$ADDITIONAL_ARGS --tls-san $(read_input "Enter the SAN:")"
    fi
    if yes_or_no "Do you want to add another additional arguments?"
    then
        ADDITIONAL_ARGS="$ADDITIONAL_ARGS $(read_input "Enter the additional arguments:")"
    fi
    echo "$ADDITIONAL_ARGS"
}

gather_master_params() {
    local MASTER_URL TOKEN
    MASTER_URL=$(read_required_input "Enter the master URL(e.g. https://192.168.1.1:6443):" "Master URL is required.") || return 1
    TOKEN=$(read_required_input "Enter the token:" "Token is required.") || return 1
    echo " --server=$MASTER_URL --token=$TOKEN"
}

# Pipes the official k3s installer into sh. The arguments are split on
# whitespace only - never evaluated - so input cannot inject shell syntax.
run_k3s_installer() {
    local -a INSTALLER_ARGS
    read -ra INSTALLER_ARGS <<<"$*"
    echo_message "Running command: curl -sfL $K3S_INSTALL_URL | sh -s - ${INSTALLER_ARGS[*]}"
    curl -sfL "$K3S_INSTALL_URL" | sh -s - "${INSTALLER_ARGS[@]}"
    exit_if_error "Failed to install k3s"
    return 0
}

install_k3s_initial_master() {
    echo_message "Installing initial k3s master node..."
    run_k3s_installer server --cluster-init --flannel-backend "$K3S_FLANNEL_BACKEND" "$1"
}

show_k3s_token() {
    echo_message "Getting token..."
    local K3S_TOKEN
    K3S_TOKEN=$(sudo cat /var/lib/rancher/k3s/server/token)
    echo_message "K3S_TOKEN: $(magenta """$K3S_TOKEN""")"
    echo_message "Use it for joining agents and other master nodes to the cluster."
}

install_k3s_master() {
    echo_message "Installing k3s master node..."
    run_k3s_installer server "$1" --flannel-backend "$K3S_FLANNEL_BACKEND" "$2"
}

install_k3s_agent() {
    echo_message "Installing k3s agent node..."
    run_k3s_installer agent "$1"
}

start_k3s_master() {
    run_privileged "Starting k3s..." "Failed to start k3s" systemctl start k3s
}

stop_k3s_master() {
    run_privileged "Stopping k3s..." "Failed to stop k3s" systemctl stop k3s
}

start_k3s_agent() {
    run_privileged "Starting k3s agent..." "Failed to start k3s agent" systemctl start k3s-agent
}

stop_k3s_agent() {
    run_privileged "Stopping k3s agent..." "Failed to stop k3s agent" systemctl stop k3s-agent
}

killall_k3s() {
    run_privileged "Killing k3s..." "Failed to kill k3s" /usr/local/bin/k3s-killall.sh
}

uninstall_k3s() {
    if yes_or_no "Do you want to uninstall k3s?"
    then
        run_privileged "Uninstalling k3s..." "Failed to uninstall k3s" /usr/local/bin/k3s-uninstall.sh
    fi
}
