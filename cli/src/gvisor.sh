#!/bin/bash

GVISOR_KEY_URL="https://gvisor.dev/archive.key"
GVISOR_KEYRING="/usr/share/keyrings/gvisor-archive-keyring.gpg"
GVISOR_REPO_URL="https://storage.googleapis.com/gvisor/releases"
GVISOR_APT_SOURCE="/etc/apt/sources.list.d/gvisor.list"
K3S_CONTAINERD_DIR="/var/lib/rancher/k3s/agent/etc/containerd"
K3S_SYSTEMD_DIR="/etc/systemd/system"

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

# The service running k3s on this node: agents run k3s-agent, servers run k3s.
k3s_service_name() {
    if [[ -f "$K3S_SYSTEMD_DIR/k3s-agent.service" ]]
    then
        echo "k3s-agent"
    else
        echo "k3s"
    fi
}

register_gvisor_to_k3s() {
    local CONFIG="$K3S_CONTAINERD_DIR/config.toml" SERVICE
    SERVICE="$(k3s_service_name)"
    echo_message "Checking containerd configuration..."
    sudo grep -qs ".containerd.runtimes.runsc" "$CONFIG.tmpl" && return 0

    echo_message "Copying containerd config to template..."
    sudo cp "$CONFIG" "$CONFIG.tmpl"
    exit_if_error "Failed to copy the containerd configuration"

    echo_info "Adding runsc runtime to containerd configuration..."
    runsc_runtime_config | sudo tee -a "$CONFIG.tmpl"
    exit_if_error "Failed to add runsc runtime to containerd configuration"

    run_privileged "Restarting $SERVICE..." "Failed to register gVisor to k3s" systemctl restart "$SERVICE"
}

# Installs gVisor and wires it into k3s - the pair every node install needs.
setup_gvisor() {
    install_gvisor
    register_gvisor_to_k3s
}
