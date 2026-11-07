#!/bin/bash

K3S_INSTALL_URL="https://get.k3s.io"
K3S_FLANNEL_BACKEND="wireguard-native"

# Set by the install commands from the repeatable --tls-san and --disable flags.
TLS_SANS=()
DISABLE_COMPONENTS=()

check_k3s_installed() {
    echo_message "Checking if k3s is already installed..."
    if check_if_command_exists "k3s"
    then
        echo_info "k3s is already installed."
        report_state "already installed"
        if [[ "$1" -ne "1" ]]
        then
            exit 0
        fi
    fi
}

# Builds the extra k3s server arguments. Values given as flags are used as is
# and never prompted for; with --yes and no flags nothing is asked or added;
# otherwise the user is asked interactively.
# Usage: gather_additional_args [--yes] [EXTRA_ARGS]
gather_additional_args() {
    local YES="$1" EXTRA="$2" ADDITIONAL_ARGS="" ITEM
    if ((${#DISABLE_COMPONENTS[@]} + ${#TLS_SANS[@]})) || [[ -n "$EXTRA" ]]
    then
        for ITEM in "${DISABLE_COMPONENTS[@]}"
        do
            ADDITIONAL_ARGS="$ADDITIONAL_ARGS --disable=$ITEM"
        done
        for ITEM in "${TLS_SANS[@]}"
        do
            ADDITIONAL_ARGS="$ADDITIONAL_ARGS --tls-san $ITEM"
        done
        if [[ -n "$EXTRA" ]]
        then
            ADDITIONAL_ARGS="$ADDITIONAL_ARGS $EXTRA"
        fi
        echo "$ADDITIONAL_ARGS"
        return 0
    fi
    [[ "$(is_automatic_mode "$YES")" == "0" ]] && return 0

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

# Prints the cluster URL to join: the flag value, else a prompt (never with --yes).
# Usage: resolve_server_url SERVER [--yes]
resolve_server_url() {
    if [[ -n "$1" ]]
    then
        echo "$1"
        return 0
    fi
    if [[ "$(is_automatic_mode "$2")" == "0" ]]
    then
        echo_error "--server is required with --yes." >&2
        return 1
    fi
    read_required_input "Enter the master URL(e.g. https://192.168.1.1:6443):" "Master URL is required."
}

# Prints the join token: the K3S_TOKEN environment variable, else a prompt
# (never with --yes). There is deliberately no --token flag: it would show in
# ps and shell history.
# Usage: resolve_token [--yes]
resolve_token() {
    if [[ -n "${K3S_TOKEN:-}" ]]
    then
        echo "$K3S_TOKEN"
        return 0
    fi
    if [[ "$(is_automatic_mode "$1")" == "0" ]]
    then
        echo_error "K3S_TOKEN is required with --yes." >&2
        return 1
    fi
    read_required_input "Enter the token:" "Token is required."
}

# Pipes the official k3s installer into sh. The arguments are split on
# whitespace only - never evaluated - so input cannot inject shell syntax.
# The join token is not an argument: the installer reads K3S_TOKEN from the
# environment, which keeps it out of ps and the logged command line.
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

# Usage: install_k3s_master SERVER_URL [ADDITIONAL_ARGS]; K3S_TOKEN exported.
install_k3s_master() {
    echo_message "Installing k3s master node..."
    run_k3s_installer server --server "$1" --flannel-backend "$K3S_FLANNEL_BACKEND" "$2"
}

# Usage: install_k3s_agent SERVER_URL; K3S_TOKEN exported.
install_k3s_agent() {
    echo_message "Installing k3s agent node..."
    K3S_URL="$1" run_k3s_installer agent
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

# Usage: uninstall_k3s [--yes]
uninstall_k3s() {
    if yes_or_no "Do you want to uninstall k3s?" "$(is_automatic_mode "$1")"
    then
        run_privileged "Uninstalling k3s..." "Failed to uninstall k3s" /usr/local/bin/k3s-uninstall.sh
    fi
}
