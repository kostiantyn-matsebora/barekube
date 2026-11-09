#!/bin/bash

# The node-agent is the small daemon the optional barekube console talks to. It
# runs under an account of its own that cannot log in and has no sudo. It offers
# a fixed set of actions (reboot, power off, start and stop k3s, cordon, drain)
# and never runs a command it is sent. Everything below is idempotent: running
# it again changes only what differs.

NODE_AGENT_USER="barekube-agent"
NODE_AGENT_BIN="/usr/local/bin/barekube-node-agent"
NODE_AGENT_CONF_DIR="/etc/barekube-agent"
NODE_AGENT_UNIT="/etc/systemd/system/barekube-node-agent.service"
NODE_AGENT_POLKIT_RULE="/etc/polkit-1/rules.d/49-barekube-agent.rules"
NODE_AGENT_SUDOERS="/etc/sudoers.d/barekube-agent-etcd-snapshot"
NODE_AGENT_VIP_UNIT="/etc/systemd/system/barekube-vip-bootstrap.service"
NODE_AGENT_VIP_HELPER="/usr/local/sbin/barekube-vip-bootstrap"
NODE_AGENT_PORT="9091"
NODE_AGENT_ETCD_METRICS=""
# The actions the console may start on this node, comma separated. Empty means
# the default of the node: everything on a master, all but power off on an agent.
NODE_AGENT_OPERATIONS=""
NODE_AGENT_ALL_OPERATIONS="reboot standby rejoin poweroff"
K3S_CONFIG_FILE="/etc/rancher/k3s/config.yaml"
K3S_KUBECONFIG_FILE="/etc/rancher/k3s/k3s.yaml"
NODE_AGENT_RELEASE_URL=""

# The node-agent is released with the CLI, so it has the version of this script.
node_agent_release_url() {
    if [[ -n "$NODE_AGENT_RELEASE_URL" ]]
    then
        echo "$NODE_AGENT_RELEASE_URL"
    else
        echo "https://github.com/kostiantyn-matsebora/barekube/releases/download/v$(barekube --version)"
    fi
}

# Prints the architecture name the release files use.
node_agent_arch() {
    case "$(uname -m)" in
        x86_64) echo amd64 ;;
        aarch64|arm64) echo arm64 ;;
        armv7l|armv6l) echo arm ;;
        *) echo_error "Unsupported architecture: $(uname -m)" >&2; return 1 ;;
    esac
}

# A master has the k3s server kubeconfig, an agent has not.
node_is_master() {
    [[ -f "$K3S_KUBECONFIG_FILE" ]]
}

# Usage: resolve_console_token [--yes]
resolve_console_token() {
    if [[ -n "${CONSOLE_NODE_TOKEN:-}" ]]
    then
        echo "$CONSOLE_NODE_TOKEN"
        return 0
    fi
    if [[ "$(is_automatic_mode "$1")" == "0" ]]
    then
        echo_error "CONSOLE_NODE_TOKEN is required with --yes." >&2
        return 1
    fi
    read_required_input "Enter the console node token:" "Token is required."
}

# Only plain host names and addresses get into a unit or a script.
# Usage: valid_address VALUE
valid_address() {
    [[ "$1" =~ ^[A-Za-z0-9.-]+$ ]]
}

# Usage: valid_console_addr URL
valid_console_addr() {
    [[ "$1" =~ ^https?://[A-Za-z0-9.-]+(:[0-9]+)?$ ]]
}

# Usage: valid_interval VALUE (a Go duration such as 5s or 1m)
valid_interval() {
    [[ "$1" =~ ^[0-9]+(ms|s|m|h)$ ]]
}

# Tokens end up in a file read by systemd: no line breaks or spaces.
# Usage: valid_token VALUE
valid_token() {
    [[ "$1" =~ ^[A-Za-z0-9._~+/=-]{16,}$ ]]
}

# Usage: valid_port VALUE
valid_port() {
    [[ "$1" =~ ^[0-9]+$ ]] && ((10#$1 >= 1 && 10#$1 <= 65535))
}

# Usage: valid_url VALUE
valid_url() {
    [[ "$1" =~ ^https?://[A-Za-z0-9.-]+(:[0-9]+)?(/[A-Za-z0-9._/-]*)?$ ]]
}

# A comma separated list of known operations, without repeats.
# Usage: valid_operations VALUE
valid_operations() {
    local ITEM SEEN=" "
    [[ -n "$1" && "$1" != ,* && "$1" != *, && "$1" != *,,* ]] || return 1
    for ITEM in ${1//,/ }
    do
        [[ " $NODE_AGENT_ALL_OPERATIONS " == *" $ITEM "* && "$SEEN" != *" $ITEM "* ]] || return 1
        SEEN="$SEEN$ITEM "
    done
}

# Whether the operations of this node include one. Empty means the defaults.
# Usage: node_agent_allows OPERATION IS_MASTER(0|1)
node_agent_allows() {
    local OPERATIONS=",${NODE_AGENT_OPERATIONS// /},"
    if [[ -z "$NODE_AGENT_OPERATIONS" ]]
    then
        [[ "$1" != "poweroff" || "$2" == "1" ]]
        return
    fi
    # Power off needs a master whatever is asked: nothing can power an agent back on.
    [[ "$1" != "poweroff" || "$2" == "1" ]] && [[ "$OPERATIONS" == *",$1,"* ]]
}

# Downloads the binary of this architecture with its checksum and signature
# into DIR, and verifies both. Prints the path of the verified binary.
# Usage: fetch_node_agent DIR [--skip-signature-check]
fetch_node_agent() {
    local DIR="$1" SKIP="$2" ARCH FILE URL EXPECTED NAME
    ARCH="$(node_agent_arch)" || return 1
    FILE="node-agent-linux-$ARCH"
    URL="$(node_agent_release_url)"
    for NAME in "$FILE" node-agent.SHA256SUMS "$FILE.sigstore.json"
    do
        wget -q "$URL/$NAME" -O "$DIR/$NAME" || { echo_error "Failed to download $NAME" >&2; return 1; }
    done
    EXPECTED="$(grep " $FILE\$" "$DIR/node-agent.SHA256SUMS")"
    if [[ -z "$EXPECTED" ]] || ! (cd "$DIR" && echo "$EXPECTED" | sha256sum --check --status)
    then
        echo_error "Checksum verification failed" >&2
        return 1
    fi
    if [[ "$SKIP" == "--skip-signature-check" ]]
    then
        echo_info "Skipping signature verification; only the checksum was verified." >&2
    else
        if ! check_if_command_exists cosign
        then
            echo_error "cosign is required to verify the signature. Install it (https://docs.sigstore.dev/cosign/system_config/installation/) or pass --skip-signature-check." >&2
            return 1
        fi
        cosign verify-blob --bundle "$DIR/$FILE.sigstore.json" \
            --certificate-identity-regexp "$BAREKUBE_SIGNER_IDENTITY" \
            --certificate-oidc-issuer "$BAREKUBE_SIGNER_ISSUER" "$DIR/$FILE" >&2 \
            || { echo_error "Signature verification failed" >&2; return 1; }
    fi
    echo "$DIR/$FILE"
}

# Usage: install_node_agent_binary FILE
install_node_agent_binary() {
    run_privileged "Installing the node-agent binary..." "Failed to install the node-agent binary" \
        install -m 0755 -o root -g root "$1" "$NODE_AGENT_BIN"
}

create_node_agent_user() {
    if id -u "$NODE_AGENT_USER" &>/dev/null
    then
        echo_message "User $NODE_AGENT_USER already exists."
        return 0
    fi
    # nologin: nothing ever logs in as this account. systemd runs the daemon as it.
    run_privileged "Creating the user $NODE_AGENT_USER..." "Failed to create the user $NODE_AGENT_USER" \
        useradd --system --no-create-home --shell /usr/sbin/nologin "$NODE_AGENT_USER"
    NODE_AGENT_CHANGED=1
}

# The admin kubeconfig is readable by root only. The agent needs it to run
# `k3s kubectl`, so the group of the agent's account gets read access. k3s
# rewrites the file on every start, so this goes through its own configuration.
# Masters only.
allow_agent_to_read_kubeconfig() {
    if sudo grep -q "^write-kubeconfig-group:" "$K3S_CONFIG_FILE" 2>/dev/null
    then
        echo_message "k3s already makes the kubeconfig readable by the group."
        return 0
    fi
    echo_message "Making the k3s kubeconfig readable by $NODE_AGENT_USER..."
    printf '\n# The barekube node-agent reads the admin kubeconfig to run k3s kubectl.\nwrite-kubeconfig-mode: "0640"\nwrite-kubeconfig-group: %s\n' \
        "$NODE_AGENT_USER" | sudo tee -a "$K3S_CONFIG_FILE" >/dev/null
    exit_if_error "Failed to configure the kubeconfig access"
    run_privileged "Restarting k3s..." "Failed to restart k3s" systemctl restart k3s
    local TRY
    for TRY in $(seq 1 30)
    do
        sudo systemctl is-active --quiet k3s && break
        sleep 2
    done
    for TRY in 1 2 3 4 5
    do
        sudo k3s kubectl get --raw=/readyz &>/dev/null && break
        sleep 3
    done
    NODE_AGENT_CHANGED=1
}

create_node_agent_certificate() {
    if sudo test -f "$NODE_AGENT_CONF_DIR/cert.pem"
    then
        echo_message "TLS certificate already exists."
    else
        echo_message "Generating the TLS certificate..."
        sudo mkdir -p "$NODE_AGENT_CONF_DIR"
        sudo openssl req -x509 -newkey rsa:2048 -nodes -keyout "$NODE_AGENT_CONF_DIR/key.pem" \
            -out "$NODE_AGENT_CONF_DIR/cert.pem" -days 3650 -subj "/CN=$(hostname)" &>/dev/null
        exit_if_error "Failed to generate the TLS certificate"
        NODE_AGENT_CHANGED=1
    fi
    sudo chown -R "$NODE_AGENT_USER:$NODE_AGENT_USER" "$NODE_AGENT_CONF_DIR"
    sudo chmod 600 "$NODE_AGENT_CONF_DIR/key.pem"
}

# Prints the SHA-256 fingerprint of the certificate: lower case, no colons. The
# console pins it. It is not a secret.
node_agent_fingerprint() {
    sudo openssl x509 -in "$NODE_AGENT_CONF_DIR/cert.pem" -noout -fingerprint -sha256 \
        | sed 's/.*=//; s/://g' | tr 'A-F' 'a-f'
}

install_polkit() {
    if ! dpkg -s polkitd &>/dev/null
    then
        run_privileged "Installing polkit..." "Failed to install polkit" apt-get install -y polkitd
    fi
    sudo systemctl enable --now polkit &>/dev/null || true
}

# Prints the polkit rules for the operations of this node. Only the one k3s unit
# can be started, stopped or killed, and only a master may power off.
# Usage: polkit_rules K3S_UNIT IS_MASTER(0|1) HAS_VIP(0|1)
polkit_rules() {
    local UNIT="$1" MASTER="$2" VIP="$3"
    echo '// Managed by barekube. Grants a fixed, narrow set of actions to one account.'
    echo 'polkit.addRule(function(action, subject) {'
    if node_agent_allows reboot "$MASTER"
    then
        cat <<EOF
    if ((action.id == "org.freedesktop.login1.reboot" ||
         action.id == "org.freedesktop.login1.reboot-multiple-sessions") &&
        subject.user == "$NODE_AGENT_USER") {
        return polkit.Result.YES;
    }
EOF
    fi
    if node_agent_allows standby "$MASTER" || node_agent_allows rejoin "$MASTER" || node_agent_allows poweroff "$MASTER"
    then
        cat <<EOF
    if (action.id == "org.freedesktop.systemd1.manage-units" &&
        subject.user == "$NODE_AGENT_USER" &&
        action.lookup("unit") == "$UNIT.service" &&
        (action.lookup("verb") == "start" || action.lookup("verb") == "stop" || action.lookup("verb") == "kill")) {
        return polkit.Result.YES;
    }
EOF
    fi
    if node_agent_allows poweroff "$MASTER"
    then
        cat <<EOF
    if ((action.id == "org.freedesktop.login1.power-off" ||
         action.id == "org.freedesktop.login1.power-off-multiple-sessions") &&
        subject.user == "$NODE_AGENT_USER") {
        return polkit.Result.YES;
    }
EOF
    fi
    if [[ "$VIP" == "1" ]]
    then
        cat <<EOF
    if (action.id == "org.freedesktop.systemd1.manage-units" &&
        subject.user == "$NODE_AGENT_USER" &&
        action.lookup("unit") == "$(basename "$NODE_AGENT_VIP_UNIT")" &&
        (action.lookup("verb") == "start" || action.lookup("verb") == "stop")) {
        return polkit.Result.YES;
    }
EOF
    fi
    echo "});"
}

# Writes stdin to FILE with MODE as root, and sets NODE_AGENT_CHANGED when the
# file was different. The check runs in the caller's shell, not in the pipe.
# Usage: write_managed_file FILE MODE < content
write_managed_file() {
    local FILE="$1" MODE="$2" CONTENT
    CONTENT="$(cat)"
    if [[ "$(sudo cat "$FILE" 2>/dev/null)" == "$CONTENT" ]]
    then
        return 0
    fi
    sudo mkdir -p "$(dirname "$FILE")"
    printf '%s\n' "$CONTENT" | sudo tee "$FILE" >/dev/null
    exit_if_error "Failed to write $FILE"
    sudo chmod "$MODE" "$FILE"
    NODE_AGENT_CHANGED=1
}

# Usage: install_polkit_rules K3S_UNIT IS_MASTER HAS_VIP
install_polkit_rules() {
    echo_message "Installing the polkit rules..."
    write_managed_file "$NODE_AGENT_POLKIT_RULE" 0644 < <(polkit_rules "$1" "$2" "$3")
    sudo systemctl reload polkit 2>/dev/null || sudo systemctl restart polkit
}

# The cold-start helper: holds the API address by hand while the cluster network
# and the virtual IP wait for each other after a full power-off. The console
# starts it only when it has proved that deadlock. Only the two addresses are
# baked in, so the agent can start the unit but never chooses what it announces.
# Usage: install_vip_bootstrap VIP GATEWAY
install_vip_bootstrap() {
    local VIP="$1" GATEWAY="$2"
    write_managed_file "$NODE_AGENT_VIP_HELPER" 0755 < <(sed "s/@GATEWAY@/$GATEWAY/g; s/@VIP@/$VIP/g" <<'HELPER'
#!/bin/sh
# Managed by barekube. Holds the API VIP by hand during a cold start, until
# kube-vip holds it itself. Fixed address, no arguments but up|down.
set -eu
VIP=@VIP@
GATEWAY=@GATEWAY@
STATE=/run/barekube-vip-bootstrap.dev
case "${1:-}" in
up)
  dev=$(ip -o -4 route get "$GATEWAY" | awk '{ for (i = 1; i < NF; i++) if ($i == "dev") { print $(i + 1); exit } }')
  [ -n "$dev" ] || { echo "no interface towards $GATEWAY" >&2; exit 1; }
  ip addr add "$VIP/32" dev "$dev"
  echo "$dev" > "$STATE"
  if command -v arping >/dev/null 2>&1; then arping -U -c 2 -I "$dev" "$VIP" >/dev/null 2>&1 || true; fi
  ;;
down)
  dev=$(cat "$STATE" 2>/dev/null || true)
  if [ -n "$dev" ]; then ip addr del "$VIP/32" dev "$dev" || true; fi
  rm -f "$STATE"
  ;;
*)
  echo "usage: $0 up|down" >&2
  exit 2
  ;;
esac
HELPER
)
    write_managed_file "$NODE_AGENT_VIP_UNIT" 0644 < <(cat <<UNIT
[Unit]
Description=Temporary kube-apiserver address for a cold start (barekube console)

# Never enabled: the console starts it after proving the deadlock and stops it
# again once kube-vip holds the address.
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=$NODE_AGENT_VIP_HELPER up
ExecStop=$NODE_AGENT_VIP_HELPER down
UNIT
)
}

remove_vip_bootstrap() {
    sudo rm -f "$NODE_AGENT_VIP_UNIT" "$NODE_AGENT_VIP_HELPER"
}

# The one privileged command that is not a polkit action: a local etcd snapshot,
# taken first in a cluster shutdown. sudo matches the argument list exactly.
# Masters only.
install_etcd_snapshot_sudoers() {
    local RULE
    RULE="$NODE_AGENT_USER ALL=(root) NOPASSWD: /usr/local/bin/k3s etcd-snapshot save --name console-shutdown"
    if [[ "$(sudo cat "$NODE_AGENT_SUDOERS" 2>/dev/null)" == "$RULE" ]]
    then
        return 0
    fi
    echo_message "Installing the etcd snapshot rule..."
    printf '%s\n' "$RULE" | sudo tee "$NODE_AGENT_SUDOERS.tmp" >/dev/null
    sudo chmod 440 "$NODE_AGENT_SUDOERS.tmp"
    if ! sudo visudo -cf "$NODE_AGENT_SUDOERS.tmp" &>/dev/null
    then
        sudo rm -f "$NODE_AGENT_SUDOERS.tmp"
        echo_error "The sudoers rule is invalid, not installed"
        exit 1
    fi
    sudo mv "$NODE_AGENT_SUDOERS.tmp" "$NODE_AGENT_SUDOERS"
    NODE_AGENT_CHANGED=1
}

# The token goes to a file only root can read, not into the unit, which anyone
# on the machine can read.
# Usage: write_node_agent_token TOKEN
write_node_agent_token() {
    write_managed_file "$NODE_AGENT_CONF_DIR/env" 0600 < <(printf 'NODE_AGENT_TOKEN=%s\n' "$1")
}

# The argument for the etcd metrics URL, when one is set.
etcd_metrics_argument() {
    [[ -z "$NODE_AGENT_ETCD_METRICS" ]] || printf ' -etcd-metrics-url %s' "$NODE_AGENT_ETCD_METRICS"
}

# Usage: write_node_agent_unit K3S_UNIT NODE_NAME CONSOLE_ADDR HEARTBEAT_INTERVAL
write_node_agent_unit() {
    local UNIT="$1" NAME="$2" CONSOLE="$3" INTERVAL="$4"
    write_managed_file "$NODE_AGENT_UNIT" 0644 < <(cat <<EOF
[Unit]
Description=barekube console node-agent
After=network.target

[Service]
User=$NODE_AGENT_USER
EnvironmentFile=$NODE_AGENT_CONF_DIR/env
ExecStart=$NODE_AGENT_BIN -addr :$NODE_AGENT_PORT -cert $NODE_AGENT_CONF_DIR/cert.pem -key $NODE_AGENT_CONF_DIR/key.pem -kubectl-bin k3s -k3s-unit $UNIT -node-name $NAME -console-addr $CONSOLE -heartbeat-interval $INTERVAL$(etcd_metrics_argument)
Restart=on-failure

[Install]
WantedBy=multi-user.target
EOF
)
}

start_node_agent() {
    run_privileged "Reloading systemd..." "Failed to reload systemd" systemctl daemon-reload
    sudo systemctl enable barekube-node-agent.service &>/dev/null
    # restart, not "enable --now": that does nothing for a service that already
    # runs, and a changed binary or argument list must take effect.
    run_privileged "Starting the node-agent..." "Failed to start the node-agent" systemctl restart barekube-node-agent.service
    sleep 1
    sudo systemctl is-active --quiet barekube-node-agent.service
    exit_if_error "The node-agent is not running"
}

# Everything for one node. Sets NODE_AGENT_CHANGED to 1 when something differed.
# Usage: setup_node_agent BINARY TOKEN NODE_NAME CONSOLE_ADDR INTERVAL VIP GATEWAY
setup_node_agent() {
    local BINARY="$1" TOKEN="$2" NAME="$3" CONSOLE="$4" INTERVAL="$5" VIP="$6" GATEWAY="$7"
    local UNIT=k3s-agent MASTER=0 HAS_VIP=0
    NODE_AGENT_CHANGED=""
    if node_is_master
    then
        UNIT=k3s
        MASTER=1
        [[ -n "$VIP" ]] && HAS_VIP=1
    fi

    if ! sudo cmp -s "$BINARY" "$NODE_AGENT_BIN"
    then
        install_node_agent_binary "$BINARY"
        NODE_AGENT_CHANGED=1
    fi
    create_node_agent_user
    [[ "$MASTER" == "1" ]] && allow_agent_to_read_kubeconfig
    create_node_agent_certificate
    install_polkit
    install_polkit_rules "$UNIT" "$MASTER" "$HAS_VIP"
    if [[ "$HAS_VIP" == "1" ]]
    then
        install_vip_bootstrap "$VIP" "$GATEWAY"
    else
        remove_vip_bootstrap
    fi
    if [[ "$MASTER" == "1" ]] && node_agent_allows poweroff 1
    then
        install_etcd_snapshot_sudoers
    else
        sudo rm -f "$NODE_AGENT_SUDOERS"
    fi
    write_node_agent_token "$TOKEN"
    write_node_agent_unit "$UNIT" "$NAME" "$CONSOLE" "$INTERVAL"
    # An unchanged installation is left alone, unless the service is not running.
    if [[ -n "$NODE_AGENT_CHANGED" ]] || ! sudo systemctl is-active --quiet barekube-node-agent.service
    then
        start_node_agent
        NODE_AGENT_CHANGED=1
    fi
}

# Removes the node-agent and everything it was given.
remove_node_agent() {
    sudo systemctl disable --now barekube-node-agent.service &>/dev/null || true
    sudo systemctl stop barekube-vip-bootstrap.service &>/dev/null || true
    sudo rm -f "$NODE_AGENT_UNIT" "$NODE_AGENT_BIN" "$NODE_AGENT_POLKIT_RULE" "$NODE_AGENT_SUDOERS" \
        "$NODE_AGENT_VIP_UNIT" "$NODE_AGENT_VIP_HELPER"
    sudo rm -rf "$NODE_AGENT_CONF_DIR"
    sudo systemctl daemon-reload
    sudo userdel "$NODE_AGENT_USER" 2>/dev/null || true
}
