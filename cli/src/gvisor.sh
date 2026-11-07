#!/bin/bash

GVISOR_KEY_URL="https://gvisor.dev/archive.key"
GVISOR_KEYRING="/usr/share/keyrings/gvisor-archive-keyring.gpg"
GVISOR_REPO_URL="https://storage.googleapis.com/gvisor/releases"
GVISOR_APT_SOURCE="/etc/apt/sources.list.d/gvisor.list"
K3S_CONTAINERD_DIR="/var/lib/rancher/k3s/agent/etc/containerd"
# k3s merges every file in this directory into its configuration. The file
# belongs to barekube, so the default can be switched on and off cleanly.
K3S_CONFIG_DIR="/etc/rancher/k3s/config.yaml.d"
GVISOR_DEFAULT_CONFIG="$K3S_CONFIG_DIR/barekube-gvisor-default.yaml"

install_gvisor_prerequisites() {
    run_privileged "Installing gVisor dependencies..." "Failed to install gVisor dependencies" \
        apt install -y apt-transport-https ca-certificates curl gnupg
}

add_gvisor_apt_key() {
    echo_message "Adding gVisor key..."
    curl -fsSL "$GVISOR_KEY_URL" | sudo gpg --dearmor --yes -o "$GVISOR_KEYRING"
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

# The runtime stanza for containerd 1.x (config version 2), which older k3s uses.
runsc_runtime_config_v2() {
    cat <<'EOF'
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]
  runtime_type = "io.containerd.runsc.v1"
EOF
}

# The same for containerd 2.x (config version 3), which current k3s uses. The
# CRI plugin has a different name there; the version 2 stanza is silently
# ignored by it, which leaves no runsc runtime registered.
runsc_runtime_config_v3() {
    cat <<'EOF'
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runsc]
  runtime_type = 'io.containerd.runsc.v1'
EOF
}

# Adds the runsc runtime to one k3s containerd template. A new template extends
# the configuration k3s generates through its base template, so it keeps
# working as k3s changes its defaults; an existing template is only appended to.
# Returns 0 when it changed the template, 1 when runsc was already in it.
# Usage: add_runsc_to_template FILE STANZA_FUNCTION
add_runsc_to_template() {
    local FILE="$1" STANZA="$2"
    sudo grep -qs ".containerd.runtimes.runsc" "$FILE" && return 1
    if ! sudo test -f "$FILE"
    then
        echo '{{ template "base" . }}' | sudo tee "$FILE" > /dev/null
        exit_if_error "Failed to create the containerd template $FILE"
    fi
    echo_info "Adding runsc runtime to $FILE..."
    "$STANZA" | sudo tee -a "$FILE" > /dev/null
    exit_if_error "Failed to add runsc runtime to containerd configuration"
    return 0
}

# Makes runsc the runtime of every pod that does not name a RuntimeClass on this
# node ("on"), or stops doing so ("off"). Returns 0 when it changed something,
# 1 when the node was already as asked.
# Usage: set_gvisor_default on|off
set_gvisor_default() {
    if [[ "$1" == "on" ]]
    then
        sudo grep -qsx "default-runtime: runsc" "$GVISOR_DEFAULT_CONFIG" && return 1
        run_privileged "Preparing the k3s configuration directory..." "Failed to create $K3S_CONFIG_DIR" \
            mkdir -p "$K3S_CONFIG_DIR"
        echo_info "Making gVisor the default runtime..."
        echo "default-runtime: runsc" | sudo tee "$GVISOR_DEFAULT_CONFIG" > /dev/null
        exit_if_error "Failed to write $GVISOR_DEFAULT_CONFIG"
        return 0
    fi
    sudo test -f "$GVISOR_DEFAULT_CONFIG" || return 1
    echo_info "Removing gVisor as the default runtime..."
    sudo rm -f "$GVISOR_DEFAULT_CONFIG"
    exit_if_error "Failed to remove $GVISOR_DEFAULT_CONFIG"
    return 0
}

# Registers the runsc runtime with k3s and, when asked, switches the default
# runtime; k3s is restarted once if anything changed.
# Usage: register_gvisor_to_k3s [on|off]   (empty leaves the default as it is)
register_gvisor_to_k3s() {
    local SERVICE CHANGED=1
    SERVICE="$(k3s_service_name)"
    echo_message "Checking containerd configuration..."
    run_privileged "Preparing the containerd template directory..." "Failed to create $K3S_CONTAINERD_DIR" \
        mkdir -p "$K3S_CONTAINERD_DIR"

    add_runsc_to_template "$K3S_CONTAINERD_DIR/config.toml.tmpl" runsc_runtime_config_v2 && CHANGED=0
    add_runsc_to_template "$K3S_CONTAINERD_DIR/config-v3.toml.tmpl" runsc_runtime_config_v3 && CHANGED=0
    if [[ -n "$1" ]]
    then
        set_gvisor_default "$1" && CHANGED=0
    fi
    if [[ "$CHANGED" -ne 0 ]]
    then
        echo_message "The runsc runtime is already registered."
        return 0
    fi

    run_privileged "Restarting $SERVICE..." "Failed to register gVisor to k3s" systemctl restart "$SERVICE"
}

# Installs gVisor and wires it into k3s - the pair every node install needs.
# Usage: setup_gvisor [on|off]
setup_gvisor() {
    install_gvisor
    register_gvisor_to_k3s "$1"
}
