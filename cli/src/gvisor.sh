#!/bin/bash

GVISOR_KEY_URL="https://gvisor.dev/archive.key"
GVISOR_KEYRING="/usr/share/keyrings/gvisor-archive-keyring.gpg"
GVISOR_REPO_URL="https://storage.googleapis.com/gvisor/releases"
GVISOR_APT_SOURCE="/etc/apt/sources.list.d/gvisor.list"
K3S_CONTAINERD_DIR="/var/lib/rancher/k3s/agent/etc/containerd"

install_gvisor_prerequisites() {
    run_privileged "Installing gVisor dependencies..." "Failed to install gVisor dependencies" \
        apt install -y apt-transport-https ca-certificates curl gnupg
}

add_gvisor_apt_key() {
    echo_message "Adding gVisor key..."
    curl -fsSL "$GVISOR_KEY_URL" | sudo gpg --dearmor -o "$GVISOR_KEYRING"
    exit_if_error "Failed to add gVisor key"
}

add_gvisor_apt_repository() {
    echo_message "Adding gVisor repository..."
    echo "deb [arch=$(dpkg --print-architecture) signed-by=$GVISOR_KEYRING] $GVISOR_REPO_URL release main" \
        | sudo tee "$GVISOR_APT_SOURCE" > /dev/null
    exit_if_error "Failed to add gVisor repository"
}

install_gvisor_package() {
    echo_message "Installing gVisor..."
    sudo apt-get update && sudo apt-get install -y runsc
    exit_if_error "Failed to install gVisor"
}

install_gvisor() {
    install_gvisor_prerequisites
    add_gvisor_apt_key
    add_gvisor_apt_repository
    install_gvisor_package
}

runsc_runtime_config() {
    cat <<'EOF'
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]
  runtime_type = "io.containerd.runsc.v1"
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc.options]
  TypeUrl = "io.containerd.runsc.v1.options"
  ConfigPath = "/etc/containerd/runsc.toml"
EOF
}

register_gvisor_to_k3s() {
    local CONFIG="$K3S_CONTAINERD_DIR/config.toml"
    echo_message "Checking containerd configuration..."
    grep -q ".containerd.runtimes.runsc" "$CONFIG.tmpl" && return 0

    echo_message "Copying containerd config to template..."
    cp "$CONFIG" "$CONFIG.tmpl"

    echo_info "Adding runsc runtime to containerd configuration..."
    runsc_runtime_config | sudo tee -a "$CONFIG.tmpl"
    exit_if_error "Failed to add runsc runtime to containerd configuration"

    run_privileged "Restarting k3s..." "Failed to register gVisor to k3s" systemctl restart k3s
}

# Installs gVisor and wires it into k3s - the pair every node install needs.
setup_gvisor() {
    install_gvisor
    register_gvisor_to_k3s
}
