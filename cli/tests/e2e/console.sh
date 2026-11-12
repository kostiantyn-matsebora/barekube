#!/usr/bin/env bash
# Adds the optional console to the cluster built by provision.sh: builds the
# node-agent and the console from this checkout, puts the agent on the nodes
# (with the CLI, or with the playbook, as provision.sh built the cluster), and
# starts the console on the runner with the configuration for those nodes.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

console_enabled || { log "the console is not part of this run"; exit 0; }
console_tokens
mkdir -p "$CONSOLE_DIR/bin" "$CONSOLE_CONFIG_DIR/credentials"

group "Build the node-agent and the console"
(
  cd "$ROOT/console"
  export GOTOOLCHAIN=auto CGO_ENABLED=0 GOOS=linux
  GOARCH="$(go env GOARCH)"
  go build -o "$CONSOLE_DIR/bin/node-agent-linux-$GOARCH" ./cmd/node-agent
  go build -o "$CONSOLE_DIR/bin/console" ./cmd/console
  echo "$GOARCH" >"$CONSOLE_DIR/arch"
)
ARCH="$(<"$CONSOLE_DIR/arch")"
endgroup

# The node-agent on a node, through the CLI of the script provisioner. Prints
# nothing; keeps the certificate fingerprint for the console to pin.
install_agent() {
  local n="$1" out
  # shellcheck disable=SC2046
  lxc file push "$CONSOLE_DIR/bin/node-agent-linux-$ARCH" "$n/tmp/node-agent" --mode 0755
  out="$(lxc exec "$n" --env CONSOLE_NODE_TOKEN="$CONSOLE_NODE_TOKEN" -- \
    barekube install node-agent --binary /tmp/node-agent $(console_install_flags "$n") 2>&1)" \
    || { printf '%s\n' "$out"; fail "the node-agent did not install on $n"; }
  [[ "$(printf '%s\n' "$out" | last_line)" == "installed" ]] || { printf '%s\n' "$out"; fail "$n: expected 'installed'"; }
  printf '%s\n' "$out" | sed -n 's/^fingerprint //p' >"$CONSOLE_CONFIG_DIR/credentials/$n"
  node "$n" rm -f /tmp/node-agent
}

if uses_playbooks; then
  group "Install the node-agents with the playbook"
  # Where the playbook finds the files: the archive directory is /ansible in Docker.
  if [[ "$PROVISIONER" == "ansible" ]]; then
    agent_dir_host="$PLAYBOOK_DIR/console-agents"; agent_dir="/ansible/console-agents"
    export_host="$PLAYBOOK_DIR/console-export"; export_dir="/ansible/console-export"
  else
    agent_dir_host="$CONSOLE_DIR/agents"; agent_dir="$agent_dir_host"
    export_host="$CONSOLE_DIR/export"; export_dir="$export_host"
  fi
  rm -rf "$agent_dir_host" "$export_host"
  mkdir -p "$agent_dir_host" "$export_host"
  cp "$CONSOLE_DIR/bin/node-agent-linux-$ARCH" "$agent_dir_host/"
  # One host overrides its role's defaults, one opts out; the rest is the
  # defaults of the role. Hosts are written one per line by provision.sh.
  sed -i "s|^        $CONSOLE_OVERRIDE_NODE: { ansible_host: \(.*\) }|        $CONSOLE_OVERRIDE_NODE: { ansible_host: \1, barekube_console_port: $CONSOLE_OVERRIDE_PORT, barekube_console_operations: [${CONSOLE_OVERRIDE_OPERATIONS//,/, }] }|; s|^        $CONSOLE_NO_AGENT: { ansible_host: \(.*\) }|        $CONSOLE_NO_AGENT: { ansible_host: \1, barekube_console_agent: false }|" "$INVENTORY_FILE"
  inventory_set barekube_console_agent true
  inventory_set barekube_console_address "$(console_url)"
  # No token in the inventory: the playbook makes one, prints it and puts it on the agents.
  inventory_set barekube_node_agent_dir "$agent_dir"
  inventory_set barekube_console_export_dir "$export_dir"
  grep -q "$CONSOLE_OVERRIDE_PORT" "$INVENTORY_FILE" || fail "the override did not reach the inventory"
  run_playbook >"$STATE_DIR/logs/console-playbook.log" 2>&1 || { tail -80 "$STATE_DIR/logs/console-playbook.log"; fail "the playbook failed"; }
  # The token the playbook made is the one on every agent, and is in its output.
  made="$(lxc exec master-1 -- sed -n 's/^NODE_AGENT_TOKEN=//p' /etc/barekube-agent/env)"
  [[ "$made" =~ ^[A-Za-z0-9]{40}$ ]] || fail "the playbook did not make a node token"
  grep -qF "$made" "$STATE_DIR/logs/console-playbook.log" || fail "the playbook did not show the token it made"
  printf '%s\n' "$made" >"$CONSOLE_DIR/node-token"
  console_tokens
  rm -rf "$CONSOLE_CONFIG_DIR"
  cp -r "$export_host" "$CONSOLE_CONFIG_DIR"
  endgroup
  cp "$CONSOLE_CONFIG_DIR/config.yaml" "$CONSOLE_DIR/config.yaml"
else
  group "Install the node-agents with the barekube CLI"
  for n in "${CONSOLE_NODES[@]}"; do install_agent "$n"; done
  {
    echo "nodeAgent: { mock: false, credentialsDir: /secrets/node-agent }"
    echo 'listenAddr: ":8080"'
    echo "nodes:"
    for n in "${CONSOLE_NODES[@]}"; do
      role=agent; console_is_master "$n" && role=master
      echo "  - { name: $n, ip: $(node_ip "$n"), role: $role, nodeAgentPort: $(console_node_port "$n"), operations: [$(console_node_operations "$n" | sed "s/,/, /g")] }"
    done
  } >"$CONSOLE_DIR/config.yaml"
  endgroup
fi

# The console reads the fingerprints from where the file says; here, on the runner.
sed -i "s|/secrets/node-agent|$CONSOLE_CONFIG_DIR/credentials|" "$CONSOLE_DIR/config.yaml"
cat "$CONSOLE_DIR/config.yaml"

group "Start the console"
console_start
endgroup
log "the console runs at $(console_url) with ${#CONSOLE_NODES[@]} node-agents"
