#!/usr/bin/env bash
# Shared helpers for the end-to-end cluster test. The "nodes" are LXD system
# containers: real systemd machines with their own IPs, on one GitHub runner.

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$E2E_DIR/../../.." && pwd)"
STATE_DIR="${E2E_STATE_DIR:-/tmp/barekube-e2e}"
mkdir -p "$STATE_DIR/logs"

MASTERS=(master-1 master-2 master-3)
AGENTS=(agent-1 agent-2)
NODES=("${MASTERS[@]}" "${AGENTS[@]}")

# How the cluster is built: "script" runs the barekube commands one node at a
# time, "ansible" runs the playbook in orchestration/ansible.
PROVISIONER="${E2E_PROVISIONER:-script}"

# Whether gVisor is the default runtime of the cluster (the matrix of the E2E
# workflow runs both).
GVISOR_DEFAULT="${E2E_GVISOR_DEFAULT:-false}"
GVISOR_FLAGS=()
[[ "$GVISOR_DEFAULT" == "true" ]] && GVISOR_FLAGS=(--gvisor-default)

# What the Ansible inventory defines (only the Ansible provisioners read it):
# "minimal" has the hosts and the connection and no barekube_* setting at all,
# so every default applies (nothing is disabled, gVisor is not the default);
# "full" defines every setting, optional ones included.
INVENTORY_KIND="${E2E_INVENTORY:-full}"

# Disabled on purpose, as in the README examples: the test cluster has no
# service load balancer or ingress; those belong to the layer above barekube.
K3S_FLAGS=(--disable servicelb --disable traefik)

# Whether the cluster is built with the Ansible playbooks, in either of the two
# ways a user gets them: "ansible" is the release archive run with play.sh in
# Docker, "galaxy" is the Ansible collection run with ansible-playbook.
uses_playbooks() { [[ "$PROVISIONER" == "ansible" || "$PROVISIONER" == "galaxy" ]]; }

# Whether servicelb and traefik are turned off in this cluster.
components_disabled() { ! { uses_playbooks && [[ "$INVENTORY_KIND" == "minimal" ]]; }; }

# The kubeconfig context the playbook creates: barekube-<barekube_cluster_name>.
KUBE_CONTEXT=barekube-default
if uses_playbooks && [[ "$INVENTORY_KIND" == "full" ]]; then KUBE_CONTEXT=barekube-e2e; fi

# Where the CLI of this checkout is, as the playbook sees it.
cli_path_for_playbook() {
  if [[ "$PROVISIONER" == "galaxy" ]]; then echo "$ROOT/cli/release/barekube"; else echo /ansible/barekube; fi
}

# Laid out as in the README: inventory.yml in the current directory (STATE_DIR),
# the extracted archive or the installed collections next to it. The CLI of this
# checkout is installed from a local file.
PLAYBOOK_DIR="$STATE_DIR/barekube-ansible"

# run_playbook [PLAYBOOK [ARGS...]]: runs a playbook (site.yml by default)
# against the test nodes over SSH, exactly as the README describes. The cluster
# is added to the runner's ~/.kube/config.
run_playbook() {
  local playbook="${1:-site.yml}"
  shift || true
  # Only what the test machines need goes on the command line (no signing
  # on a CI build, the local CLI); the settings under test are in the
  # inventory. A full inventory carries these too.
  local settings=()
  if [[ "$INVENTORY_KIND" == "minimal" ]]; then
    settings=(
      -e barekube_verify_signature=false
      -e barekube_show_output=true
      -e "barekube_cli_path=$(cli_path_for_playbook)"
    )
  fi
  (
    cd "$STATE_DIR"
    if [[ "$PROVISIONER" == "galaxy" ]]; then
      ANSIBLE_COLLECTIONS_PATH="$STATE_DIR/collections" ansible-playbook -i inventory.yml \
        "kostiantyn_matsebora.barekube.${playbook%.yml}" "$@" "${settings[@]}"
    else
      BAREKUBE_USE_DOCKER=1 "$PLAYBOOK_DIR/play.sh" "$playbook" "$@" "${settings[@]}"
    fi
  )
}

# --- Editing the inventory, as a user does on day two ----------------------
INVENTORY_FILE="$STATE_DIR/inventory.yml"

# inventory_set KEY VALUE: sets (or changes) a barekube_* setting under all.vars,
# which is the last block of the inventory the test writes.
inventory_set() {
  inventory_unset "$1"
  printf '    %s: %s\n' "$1" "$2" >>"$INVENTORY_FILE"
}

# inventory_unset KEY: removes a setting, so its default applies again.
inventory_unset() { sed -i "/^    $1:/d" "$INVENTORY_FILE"; }

# inventory_drop_host NODE: takes a host out of the inventory.
inventory_drop_host() { sed -i "/^        $1: /d" "$INVENTORY_FILE"; }

# inventory_add_host GROUP NODE: puts a host at the end of masters or agents.
inventory_add_host() {
  local group="$1" name="$2" before line
  [[ "$group" == "masters" ]] && before="    agents:" || before="  vars:"
  line="        $name: { ansible_host: $(node_ip "$name") }"
  awk -v before="$before" -v line="$line" '$0 == before { print line } { print }' \
    "$INVENTORY_FILE" >"$INVENTORY_FILE.new" && mv "$INVENTORY_FILE.new" "$INVENTORY_FILE"
}

log() { printf '[%s] %s\n' "$(date +%T)" "$*"; }
group() { printf '::group::%s\n' "$*"; }
endgroup() { printf '::endgroup::\n'; }
fail() { log "FAIL: $*"; exit 1; }

# node NAME COMMAND...: runs a command as root inside a node.
node() {
  local name="$1"
  shift
  lxc exec "$name" --env DEBIAN_FRONTEND=noninteractive -- "$@"
}

# kc ARGS...: kubectl through a control-plane node (KUBE_NODE, default master-1).
kc() { node "${KUBE_NODE:-master-1}" k3s kubectl "$@"; }

# The address on the LXD network. `hostname -I` is not enough: once k3s runs,
# the CNI bridge address can be listed first.
node_ip() { node "$1" ip -4 -o addr show dev eth0 | awk '{print $4}' | cut -d/ -f1; }

# wait_for SECONDS DESCRIPTION COMMAND...
wait_for() {
  local timeout="$1" description="$2" deadline
  shift 2
  deadline=$((SECONDS + timeout))
  until "$@" >/dev/null 2>&1; do
    ((SECONDS < deadline)) || { log "timed out after ${timeout}s waiting for: $description"; return 1; }
    sleep 5
  done
  log "ok: $description"
}

node_ready() {
  [[ "$(kc get node "$1" -o 'jsonpath={.status.conditions[?(@.type=="Ready")].status}')" == "True" ]]
}

node_not_ready() { ! node_ready "$1"; }

# parallel_nodes FUNCTION NODE...: runs FUNCTION NODE for every node at once,
# prints the log of each failure and fails when any failed.
parallel_nodes() {
  local fn="$1" name rc=0
  shift
  local -A pids=()
  for name in "$@"; do
    "$fn" "$name" >"$STATE_DIR/logs/$fn-$name.log" 2>&1 &
    pids[$name]=$!
  done
  for name in "$@"; do
    if wait "${pids[$name]}"; then
      log "$fn $name: ok"
    else
      log "$fn $name: FAILED"
      sed 's/^/    /' "$STATE_DIR/logs/$fn-$name.log"
      rc=1
    fi
  done
  return $rc
}

# last_line: the last non-empty line of stdin, colours removed.
last_line() { sed 's/\x1b\[[0-9;]*m//g' | awk 'NF { line = $0 } END { print line }'; }

# --- The optional console and its node agents --------------------------------
# With E2E_CONSOLE=true the console runs on the runner, and four of the five
# nodes get the node-agent (agent-1 is left without one on purpose, to prove
# that a node can opt out). master-2 overrides the defaults of its role: another
# port and no power-off. The role of a node always comes from its group.
CONSOLE="${E2E_CONSOLE:-false}"
console_enabled() { [[ "$CONSOLE" == "true" ]]; }
CONSOLE_DIR="$STATE_DIR/console"
CONSOLE_CONFIG_DIR="$CONSOLE_DIR/config"
CONSOLE_PORT=8080
CONSOLE_NO_AGENT=agent-1
CONSOLE_OVERRIDE_NODE=master-2
CONSOLE_OVERRIDE_PORT=9199
CONSOLE_OVERRIDE_OPERATIONS="reboot,standby,rejoin"
CONSOLE_NODES=()
for n in "${NODES[@]}"; do [[ "$n" == "$CONSOLE_NO_AGENT" ]] || CONSOLE_NODES+=("$n"); done

# The address of the runner as the nodes see it: the LXD bridge.
console_host_ip() { lxc network get lxdbr0 ipv4.address | cut -d/ -f1; }
console_url() { echo "http://$(console_host_ip):$CONSOLE_PORT"; }

console_node_port() { [[ "$1" == "$CONSOLE_OVERRIDE_NODE" ]] && echo "$CONSOLE_OVERRIDE_PORT" || echo 9091; }

console_node_operations() {
  if [[ "$1" == "$CONSOLE_OVERRIDE_NODE" ]]; then echo "$CONSOLE_OVERRIDE_OPERATIONS"
  elif [[ " ${MASTERS[*]} " == *" $1 "* ]]; then echo reboot,standby,rejoin,poweroff
  else echo reboot,standby,rejoin; fi
}

console_has_poweroff() { [[ ",$(console_node_operations "$1")," == *,poweroff,* ]]; }
console_is_master() { [[ " ${MASTERS[*]} " == *" $1 "* ]]; }

# The two secrets, made once. They are random, long, and different.
console_tokens() {
  mkdir -p "$CONSOLE_DIR"
  [[ -f "$CONSOLE_DIR/node-token" ]] || { openssl rand -hex 24 >"$CONSOLE_DIR/node-token"; openssl rand -hex 24 >"$CONSOLE_DIR/ui-token"; chmod 600 "$CONSOLE_DIR"/*-token; }
  CONSOLE_NODE_TOKEN="$(<"$CONSOLE_DIR/node-token")"
  CONSOLE_UI_TOKEN="$(<"$CONSOLE_DIR/ui-token")"
  echo "::add-mask::$CONSOLE_NODE_TOKEN"
  echo "::add-mask::$CONSOLE_UI_TOKEN"
}

# Starts the console of this checkout (built by console.sh) unless it runs.
console_start() {
  console_tokens
  if [[ -f "$CONSOLE_DIR/console.pid" ]] && kill -0 "$(<"$CONSOLE_DIR/console.pid")" 2>/dev/null; then return 0; fi
  (
    cd "$CONSOLE_DIR"
    CONSOLE_NODE_TOKEN="$CONSOLE_NODE_TOKEN" CONSOLE_UI_TOKEN="$CONSOLE_UI_TOKEN" \
      CONSOLE_DB="$CONSOLE_DIR/console.db" \
      setsid nohup "$CONSOLE_DIR/bin/console" -config "$CONSOLE_DIR/config.yaml" >>"$STATE_DIR/logs/console.log" 2>&1 &
    echo $! >"$CONSOLE_DIR/console.pid"
  )
  wait_for 60 "the console answers" curl -fsS -o /dev/null "http://127.0.0.1:$CONSOLE_PORT/login"
}

# console_api PATH [CURL ARGS...]: a call as a logged-in user of the UI.
console_api() {
  local jar="$CONSOLE_DIR/cookies" path="$1"
  shift
  [[ -s "$jar" ]] || curl -fsS -c "$jar" -o /dev/null -d "token=$CONSOLE_UI_TOKEN" "http://127.0.0.1:$CONSOLE_PORT/login"
  curl -fsS -b "$jar" "$@" "http://127.0.0.1:$CONSOLE_PORT$path"
}

# The arguments that install the agent on a node, as the script provisioner and
# the idempotence check use them.
console_install_flags() {
  echo --yes --console-addr "$(console_url)" --node-name "$1" --port "$(console_node_port "$1")" \
    --operations "$(console_node_operations "$1")"
}
