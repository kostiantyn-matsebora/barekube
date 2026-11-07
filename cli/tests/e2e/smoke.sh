#!/usr/bin/env bash
# Smoke and end-to-end checks of the cluster built by provision.sh: that the
# cluster is what barekube promises (HA control plane, WireGuard network,
# gVisor, Longhorn prerequisites) and keeps working through failures and the
# day-two commands. The destructive checks come last.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

FIRST_IP="$(<"$STATE_DIR/first-ip")"
TOKEN="$(<"$STATE_DIR/token")"
AGNHOST=registry.k8s.io/e2e-test-images/agnhost:2.53
BUSYBOX=registry.k8s.io/e2e-test-images/busybox:1.36.1-1
FAILED=()

# check NAME FUNCTION: runs one check, records failures and carries on.
check() {
  local name="$1" fn="$2" rc
  group "$name"
  # A subshell with errexit, outside any condition, so that a failing command
  # in the middle of a check fails it (errexit is ignored inside `if`).
  set +e
  (set -e; "$fn")
  rc=$?
  set -e
  if ((rc == 0)); then
    log "PASS: $name"
  else
    log "FAIL: $name"
    FAILED+=("$name")
  fi
  endgroup
}

expect_eq() { [[ "$1" == "$2" ]] || { log "expected [$2], got [$1] ($3)"; return 1; }; }

# ------------------------------------------------------------------ cluster

check_version() {
  local expected actual
  expected="$(sed -n 's/^version: *//p' "$ROOT/cli/bashly.yml" | tr -d '\r')"
  for n in "${NODES[@]}"; do
    actual="$(node "$n" barekube --version)"
    expect_eq "$actual" "$expected" "barekube version on $n" || return 1
  done
}

check_nodes_ready() {
  local ready
  ready="$(kc get nodes --no-headers | awk '$2 == "Ready"' | wc -l)"
  expect_eq "$ready" 5 "Ready nodes" || { kc get nodes -o wide; return 1; }
}

check_control_plane_is_highly_available() {
  local control etcd
  control="$(kc get nodes -l node-role.kubernetes.io/control-plane --no-headers | wc -l)"
  etcd="$(kc get nodes -l node-role.kubernetes.io/etcd=true --no-headers | wc -l)"
  expect_eq "$control" 3 "control-plane nodes" || return 1
  expect_eq "$etcd" 3 "etcd members" || return 1
  # Every master answers on its own; none is only a proxy to master-1.
  for m in "${MASTERS[@]}"; do
    KUBE_NODE="$m" kc get nodes >/dev/null || { log "$m cannot serve the API"; return 1; }
  done
}

check_disabled_components_are_absent() {
  local daemonsets
  ! kc -n kube-system get deploy traefik >/dev/null 2>&1 || { log "traefik is installed"; return 1; }
  daemonsets="$(kc -n kube-system get ds -o name)"
  [[ "$daemonsets" != *svclb* ]] || { log "servicelb is installed"; return 1; }
}

check_system_workloads_are_healthy() {
  kc -n kube-system rollout status deploy/coredns --timeout=180s
  kc -n kube-system rollout status deploy/metrics-server --timeout=180s
  local bad
  bad="$(kc get pods -A --no-headers | awk '$4 != "Running" && $4 != "Completed"')"
  [[ -z "$bad" ]] || { printf '%s\n' "$bad"; return 1; }
}

# ---------------------------------------------------------------- WireGuard

check_wireguard_mesh() {
  local peers
  for n in "${NODES[@]}"; do
    peers="$(node "$n" wg show flannel-wg peers | wc -l)"
    expect_eq "$peers" 4 "WireGuard peers of $n" || { node "$n" wg show || true; return 1; }
  done
}

# ------------------------------------------------------------ pod networking

apply_echo() {
  kc apply -f - <<MANIFEST
apiVersion: apps/v1
kind: Deployment
metadata: {name: echo}
spec:
  replicas: 5
  selector: {matchLabels: {app: echo}}
  template:
    metadata: {labels: {app: echo}}
    spec:
      topologySpreadConstraints:
        - maxSkew: 1
          topologyKey: kubernetes.io/hostname
          whenUnsatisfiable: DoNotSchedule
          labelSelector: {matchLabels: {app: echo}}
      containers:
        - name: echo
          image: $AGNHOST
          args: [netexec, --http-port=8080]
          ports: [{containerPort: 8080}]
---
apiVersion: v1
kind: Service
metadata: {name: echo}
spec:
  selector: {app: echo}
  ports: [{port: 8080}]
---
apiVersion: v1
kind: Service
metadata: {name: echo-nodeport}
spec:
  type: NodePort
  selector: {app: echo}
  ports: [{port: 8080, nodePort: 30080}]
MANIFEST
  kc rollout status deploy/echo --timeout=300s
}

echo_pods() {
  kc get pods -l app=echo -o 'jsonpath={range .items[*]}{.metadata.name} {.status.podIP} {.spec.nodeName}{"\n"}{end}'
}

check_pods_spread_over_every_node() {
  local nodes
  nodes="$(echo_pods | awk '{print $3}' | sort -u | wc -l)"
  expect_eq "$nodes" 5 "nodes running an echo pod" || { echo_pods; return 1; }
}

check_pod_to_pod_across_nodes() {
  local from_pod from_ip from_node to_pod to_ip to_node
  while read -r from_pod from_ip from_node; do
    while read -r to_pod to_ip to_node; do
      [[ "$from_pod" == "$to_pod" ]] && continue
      kc exec "$from_pod" -- /agnhost connect "$to_ip:8080" --timeout=10s >/dev/null \
        || { log "$from_node cannot reach $to_node ($to_ip)"; return 1; }
    done < <(echo_pods)
  done < <(echo_pods)
}

check_service_and_dns() {
  kc delete pod probe --ignore-not-found >/dev/null
  kc run probe --image="$BUSYBOX" --restart=Never --command -- sleep 600
  kc wait --for=condition=Ready pod/probe --timeout=180s
  kc exec probe -- nslookup kubernetes.default.svc.cluster.local
  kc exec probe -- nslookup echo.default.svc.cluster.local
  kc exec probe -- wget -qO- -T 10 http://echo.default.svc.cluster.local:8080/hostname
  kc delete pod probe --wait=false >/dev/null
}

check_nodeport_on_every_node() {
  local ip
  for n in "${NODES[@]}"; do
    ip="$(node_ip "$n")"
    curl -fsS --max-time 10 "http://$ip:30080/hostname" >/dev/null || { log "NodePort not served by $n ($ip)"; return 1; }
  done
}

# -------------------------------------------------------------------- gVisor

check_gvisor_runtime() {
  kc apply -f - <<MANIFEST
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata: {name: gvisor}
handler: runsc
---
apiVersion: v1
kind: Pod
metadata: {name: sandboxed}
spec:
  runtimeClassName: gvisor
  nodeName: agent-1
  restartPolicy: Never
  containers:
    - name: probe
      image: $BUSYBOX
      command: [sleep, "600"]
MANIFEST
  kc wait --for=condition=Ready pod/sandboxed --timeout=240s
  # gVisor answers dmesg with its own banner; a normal container shows the host's.
  local banner
  banner="$(kc exec sandboxed -- dmesg)"
  [[ "${banner,,}" == *gvisor* ]] || { printf '%s\n' "$banner" | head -5; return 1; }
  kc delete pod sandboxed --wait=false >/dev/null
}

# ------------------------------------------------------ Longhorn prerequisites

check_longhorn_prerequisites() {
  for n in "${NODES[@]}"; do
    node "$n" dpkg -s open-iscsi nfs-common >/dev/null || { log "$n lacks Longhorn packages"; return 1; }
    node "$n" systemctl is-active iscsid >/dev/null || { log "iscsid is not running on $n"; return 1; }
  done
}

# ----------------------------------------------------- failures and day two

check_idempotent_install() {
  local out
  out="$(node master-1 barekube install initial-master --yes | last_line)"
  expect_eq "$out" "already installed" "re-running initial-master" || return 1
  out="$(lxc exec agent-1 --env K3S_TOKEN="$TOKEN" -- barekube install agent-node --yes --server "https://$FIRST_IP:6443" | last_line)"
  expect_eq "$out" "already installed" "re-running agent-node" || return 1
  node_ready master-1 && node_ready agent-1
}

master_1_not_ready_via_master_2() { KUBE_NODE=master-2 node_not_ready master-1; }

check_cluster_survives_losing_a_master() {
  node master-1 barekube k3s stop
  # The control plane keeps quorum with two of three masters.
  wait_for 240 "master-1 is reported NotReady" master_1_not_ready_via_master_2 || return 1
  KUBE_NODE=master-2 kc scale deploy/echo --replicas=6 >/dev/null
  KUBE_NODE=master-2 kc rollout status deploy/echo --timeout=240s || return 1
  curl -fsS --max-time 10 "http://$(node_ip agent-1):30080/hostname" >/dev/null || { log "workloads stopped serving"; return 1; }
  node master-1 barekube k3s start
  wait_for 300 "master-1 is Ready again" node_ready master-1 || return 1
  kc scale deploy/echo --replicas=5 >/dev/null
}

check_agent_stop_and_start() {
  node agent-2 barekube k3s stop-agent
  wait_for 240 "agent-2 is NotReady" node_not_ready agent-2 || return 1
  node agent-2 barekube k3s start-agent
  wait_for 300 "agent-2 is Ready again" node_ready agent-2
}

check_agent_uninstall() {
  node agent-2 barekube k3s uninstall --yes
  ! node agent-2 test -e /usr/local/bin/k3s || { log "k3s is still installed on agent-2"; return 1; }
  kc delete node agent-2 >/dev/null
  expect_eq "$(kc get nodes --no-headers | wc -l)" 4 "nodes after removing agent-2"
}

# --------------------------------------------------------------------- runner

check "barekube is the version under test on every node" check_version
check "all five nodes are Ready" check_nodes_ready
check "the control plane is highly available (3 masters, 3 etcd members)" check_control_plane_is_highly_available
check "disabled components are not installed" check_disabled_components_are_absent
check "system workloads are healthy" check_system_workloads_are_healthy
check "every node holds a WireGuard mesh with the other four" check_wireguard_mesh
group "Deploy the echo workload"
apply_echo
endgroup
check "echo pods spread over all five nodes" check_pods_spread_over_every_node
check "pods reach each other across nodes" check_pod_to_pod_across_nodes
check "services and DNS resolve" check_service_and_dns
check "a NodePort answers on every node" check_nodeport_on_every_node
check "a pod runs sandboxed by gVisor" check_gvisor_runtime
check "Longhorn prerequisites are in place on every node" check_longhorn_prerequisites
check "re-running installs is idempotent" check_idempotent_install
check "the cluster survives losing a master" check_cluster_survives_losing_a_master
check "an agent can be stopped and started" check_agent_stop_and_start
check "an agent can be uninstalled and removed" check_agent_uninstall

if ((${#FAILED[@]})); then
  log "${#FAILED[@]} check(s) failed:"
  printf '  - %s\n' "${FAILED[@]}"
  exit 1
fi
log "all checks passed"
