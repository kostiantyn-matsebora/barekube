#!/bin/bash

update_packages() {
    echo_message "Updating packages..."
    sudo apt update

    exit_if_error "Failed to update packages"
}

install_containerd() {
    echo_message "Installing containerd..."
    sudo apt install -y containerd
    exit_if_error "Failed to install containerd"
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
    echo_message "Checking containerd configuration..."
    grep -q ".containerd.runtimes.runsc" /var/lib/rancher/k3s/agent/etc/containerd/config.toml.tmpl

    if [ $? -ne "0" ]
    then
    echo_message "Copying containerd config to template..."
    cp /var/lib/rancher/k3s/agent/etc/containerd/config.toml /var/lib/rancher/k3s/agent/etc/containerd/config.toml.tmpl
        
    echo_info "Adding runsc runtime to containerd configuration..."
sudo tee -a /var/lib/rancher/k3s/agent/etc/containerd/config.toml.tmpl <<EOF
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]
  runtime_type = "io.containerd.runsc.v1"
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc.options]
  TypeUrl = "io.containerd.runsc.v1.options"
  ConfigPath = "/etc/containerd/runsc.toml"
EOF
    exit_if_error "Failed to add runsc runtime to containerd configuration"

    echo_message "Restarting k3s..."
    sudo systemctl restart k3s
    exit_if_error "Failed to register gVisor to k3s"
    fi
    return 0
}


install_wireguard() {
    echo_message "Installing wireguard..."
    sudo apt install -y wireguard
    exit_if_error  "Failed to install wireguard"
}

check_k3s_installed() {
    echo_message "Checking if k3s is already installed..."
    check_if_command_exists "k3s"
     # shellcheck disable=SC2181
     if [[ $? -eq 0 ]]
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
    yes_or_no "Do you want to set additional parameters?"
    # shellcheck disable=SC2181
    if [[ $? -ne 0 ]]
    then
        return 0
    fi  
    yes_or_no "Do you want to disable load balancer"
    # shellcheck disable=SC2181
    if [[ $? -eq 0 ]]
    then
        ADDITIONAL_ARGS="$ADDITIONAL_ARGS --disable=servicelb"
    fi
    yes_or_no "Do you want to disable traefik"
    # shellcheck disable=SC2181
    if [[ $? -eq 0 ]]
    then
        ADDITIONAL_ARGS="$ADDITIONAL_ARGS --disable=traefik"
    fi
    yes_or_no "Do you want to set TLS SAN?"
    # shellcheck disable=SC2181
    if [[ $? -eq 0 ]]
    then
        local SAN
        SAN=$(read_input  "Enter the SAN:")
        ADDITIONAL_ARGS="$ADDITIONAL_ARGS --tls-san $SAN"
    fi
    yes_or_no "Do you want to add another additional arguments?"
    if [[ $? -eq 0 ]]
    then
        ADDITIONAL_ARGS="$ADDITIONAL_ARGS $(read_input  "Enter the additional arguments:")"
    fi
    echo "$ADDITIONAL_ARGS"
}

gather_master_params() {
    local MASTER_PARAMS
    local MASTER_URL
    MASTER_URL=$(read_input "Enter the master URL(e.g. https://192.168.1.1:6443):")
    if [[ -z "$MASTER_URL" ]]
    then
        echo_error "Master URL is required."
        exit 1
    fi    
    MASTER_PARAMS="$MASTER_PARAMS --server=$MASTER_URL"
    TOKEN=$(read_input  "Enter the token:")
    if [[ -z "$TOKEN" ]]
    then
        echo_error "Token is required."
        exit 1
    fi
    MASTER_PARAMS="$MASTER_PARAMS --token=$TOKEN"
    echo "$MASTER_PARAMS"
}

install_k3s_initial_master() {
    echo_message "Installing initial k3s master node..."
    local COMMAND
    COMMAND="curl -sfL https://get.k3s.io | sh -s - server  --cluster-init --flannel-backend wireguard-native $2"
    echo_message "Running command: $COMMAND"
    eval "$COMMAND"
    exit_if_error "Failed to install k3s"
    return 0
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
    local COMMAND
    # shellcheck disable=SC2027
    COMMAND="curl -sfL https://get.k3s.io | sh -s - server """$1""" --flannel-backend wireguard-native """$2""""
    echo_message "Running command: $COMMAND"
    eval "$COMMAND"
    exit_if_error "Failed to install k3s"
    return 0
}

install_k3s_agent() {
    echo_message "Installing k3s agent node..."
    local COMMAND
    # shellcheck disable=SC2027
    COMMAND="curl -sfL https://get.k3s.io | sh -s - agent """$1""" --flannel-backend wireguard-native """$2""""
    echo_message "Running command: $COMMAND"
    eval "$COMMAND"
    exit_if_error "Failed to install k3s"
    return 0
}


start_k3s_master() {
    echo_message "Starting k3s..."
    sudo systemctl start k3s
    exit_if_error "Failed to start k3s"
}

stop_k3s_master() {
    echo_message "Stopping k3s..."
    sudo systemctl stop k3s
    exit_if_error "Failed to stop k3s"
}

start_k3s_agent() {
    echo_message "Starting k3s agent..."
    sudo systemctl start k3s-agent
    exit_if_error "Failed to start k3s agent"
}

stop_k3s_agent() {
    echo_message "Stopping k3s agent..."
    sudo systemctl stop k3s-agent
    exit_if_error "Failed to stop k3s agent"
}

killall_k3s() {
    echo_message "Killing k3s..."
    sudo /usr/local/bin/k3s-killall.sh
    exit_if_error "Failed to kill k3s"
}

uninstall_k3s() {
    yes_or_no "Do you want to uninstall k3s?"
    if [[ $? -eq 0 ]]
    then
        echo_message "Uninstalling k3s..."
        sudo /usr/local/bin/k3s-uninstall.sh
        exit_if_error "Failed to uninstall k3s"
    fi
}
