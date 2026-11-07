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

# Whether gVisor is the default runtime of the cluster (the matrix of the E2E
# workflow runs both). The flag goes on every node, so the cluster is uniform.
GVISOR_DEFAULT="${E2E_GVISOR_DEFAULT:-false}"
GVISOR_FLAGS=()
[[ "$GVISOR_DEFAULT" == "true" ]] && GVISOR_FLAGS=(--gvisor-default)

# Disabled on purpose, as in the README examples: the test cluster has no
# service load balancer or ingress; those belong to the layer above barekube.
K3S_FLAGS=(--disable servicelb --disable traefik)

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
