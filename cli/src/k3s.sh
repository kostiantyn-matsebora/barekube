#!/bin/bash

K3S_INSTALL_URL="https://get.k3s.io"
K3S_CONTAINERD_DIR="/var/lib/rancher/k3s/agent/etc/containerd"
K3S_FLANNEL_BACKEND="wireguard-native"

update_packages() {
    run_privileged "Updating packages..." "Failed to update packages" apt update
}

install_containerd() {
    run_privileged "Installing containerd..." "Failed to install containerd" apt install -y containerd
}

install_gvisor() {
     echo_message "Installing gVisor dependencies..."
     sudo apt install -y \
        apt-transport-https \
        ca-certificates \
        curl \
        gnupg

    echo_message "Adding gVisor key..."
    curl -fsSL https://gvisor.dev/archive.key | sudo gpg --dearmor -o /usr/share/keyrings/gvisor-archive-keyring.gpg
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] https://storage.googleapis.com/gvisor/releases release main" | sudo tee /etc/apt/sources.list.d/gvisor.list > /dev/null

    exit_if_error "Failed to add gVisor key"

    echo_message "Installing gVisor..."
    sudo apt-get update && sudo apt-get install -y runsc

    exit_if_error "Failed to install gVisor"
}

register_gvisor_to_k3s() {
    local CONFIG="$K3S_CONTAINERD_DIR/config.toml"
    echo_message "Checking containerd configuration..."
    grep -q ".containerd.runtimes.runsc" "$CONFIG.tmpl" && return 0

    echo_message "Copying containerd config to template..."
    cp "$CONFIG" "$CONFIG.tmpl"

    echo_info "Adding runsc runtime to containerd configuration..."
sudo tee -a "$CONFIG.tmpl" <<EOF
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]
  runtime_type = "io.containerd.runsc.v1"
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc.options]
  TypeUrl = "io.containerd.runsc.v1.options"
  ConfigPath = "/etc/containerd/runsc.toml"
EOF
    exit_if_error "Failed to add runsc runtime to containerd configuration"

    run_privileged "Restarting k3s..." "Failed to register gVisor to k3s" systemctl restart k3s
}

install_wireguard() {
    run_privileged "Installing wireguard..." "Failed to install wireguard" apt install -y wireguard
}

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

# Pipes the official k3s installer into sh with the given arguments.
run_k3s_installer() {
    local COMMAND="curl -sfL $K3S_INSTALL_URL | sh -s - $*"
    echo_message "Running command: $COMMAND"
    eval "$COMMAND"
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
