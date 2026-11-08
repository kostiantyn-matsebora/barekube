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

# A minimal inventory disables nothing, so k3s brings its own ingress and
# service load balancer.
check_default_components_are_present() {
  kc -n kube-system rollout status deploy/traefik --timeout=180s
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
  kc wait --for=condition=Ready pod/sandboxed --timeout=240s || {
    kc describe pod sandboxed | tail -25
    return 1
  }
  # gVisor answers dmesg with its own banner; a normal container shows the host's.
  local banner
  banner="$(kc exec sandboxed -- dmesg)"
  [[ "${banner,,}" == *gvisor* ]] || { printf '%s\n' "$banner" | head -5; return 1; }
  kc delete pod sandboxed --wait=false >/dev/null
}

check_runtime_classes_exist() {
  expect_eq "$(kc get runtimeclass gvisor -o 'jsonpath={.handler}')" runsc "handler of the gvisor RuntimeClass" || return 1
  expect_eq "$(kc get runtimeclass runc -o 'jsonpath={.handler}')" runc "handler of the runc RuntimeClass" || return 1
}

# Creates a pod that names no RuntimeClass and prints the class it ended up with.
class_of_a_plain_pod() {
  kc delete pod plain --ignore-not-found --wait=true >/dev/null
  kc run plain --image="$BUSYBOX" --restart=Never --command -- sleep 600 >/dev/null || return 1
  kc get pod plain -o 'jsonpath={.spec.runtimeClassName}'
  kc delete pod plain --wait=false >/dev/null
}

# What dmesg says in a pod that names no RuntimeClass. gVisor answers with its
# own banner; runc refuses dmesg to unprivileged pods.
dmesg_in_plain_pod() {
  kc delete pod plain --ignore-not-found --wait=true >/dev/null
  kc run plain --image="$BUSYBOX" --restart=Never --command -- sleep 600 >/dev/null
  kc wait --for=condition=Ready pod/plain --timeout=240s >/dev/null || { kc describe pod plain | tail -20; return 1; }
  kc exec plain -- dmesg 2>&1 || true
  kc delete pod plain --wait=true >/dev/null
}

is_sandboxed() { [[ "${1,,}" == *gvisor* ]]; }

plain_pod_gets_class() { [[ "$(class_of_a_plain_pod)" == "$1" ]]; }

check_default_follows_the_setting() {
  local want=""
  [[ "$GVISOR_DEFAULT" == "true" ]] && want=gvisor
  wait_for 120 "a pod without a RuntimeClass gets '${want:-none}'" plain_pod_gets_class "$want" || return 1
  if [[ "$GVISOR_DEFAULT" == "true" ]]; then
    is_sandboxed "$(dmesg_in_plain_pod)" || { log "a plain pod is not sandboxed although gVisor is the default"; return 1; }
  else
    ! is_sandboxed "$(dmesg_in_plain_pod)" || { log "a plain pod is sandboxed although gVisor is not the default"; return 1; }
  fi
}

check_system_pods_are_left_alone() {
  expect_eq "$(kc get pods -n kube-system -o 'jsonpath={.items[*].spec.runtimeClassName}')" "" \
    "RuntimeClasses set on kube-system pods" || return 1
}

# Pods that need the host cannot run in a sandbox; the default must skip them.
check_host_pods_are_left_alone() {
  kc delete pod host-net --ignore-not-found --wait=true >/dev/null
  kc run host-net --image="$BUSYBOX" --restart=Never --overrides='{"spec":{"hostNetwork":true}}' \
    --command -- sleep 600 >/dev/null || return 1
  kc run privileged --image="$BUSYBOX" --restart=Never \
    --overrides='{"spec":{"containers":[{"name":"privileged","image":"'"$BUSYBOX"'","command":["sleep","600"],"securityContext":{"privileged":true}}]}}' >/dev/null || return 1
  expect_eq "$(kc get pod host-net -o 'jsonpath={.spec.runtimeClassName}')" "" "RuntimeClass of a host-network pod" || return 1
  expect_eq "$(kc get pod privileged -o 'jsonpath={.spec.runtimeClassName}')" "" "RuntimeClass of a privileged pod" || return 1
  kc delete pod host-net privileged --wait=false >/dev/null
}

check_runc_runtime_class_opts_out() {
  kc apply -f - <<MANIFEST
apiVersion: v1
kind: Pod
metadata: {name: host-runtime}
spec:
  runtimeClassName: runc
  nodeName: agent-1
  restartPolicy: Never
  containers:
    - name: probe
      image: $BUSYBOX
      command: [sleep, "600"]
MANIFEST
  kc wait --for=condition=Ready pod/host-runtime --timeout=240s || { kc describe pod host-runtime | tail -20; return 1; }
  local banner
  banner="$(kc exec host-runtime -- dmesg 2>&1 || true)"
  ! is_sandboxed "$banner" || { log "a runc pod is sandboxed"; return 1; }
  kc delete pod host-runtime --wait=false >/dev/null
}

# Switches the default to the opposite of the cluster's setting from a master
# with the install gvisor flags, checks it takes effect, then restores it.
check_gvisor_default_can_be_switched() {
  local flip restore flipped
  if [[ "$GVISOR_DEFAULT" == "true" ]]; then
    flip=--no-default restore=--default flipped=""
  else
    flip=--default restore=--no-default flipped=gvisor
  fi
  local original=""
  [[ "$GVISOR_DEFAULT" == "true" ]] && original=gvisor
  node master-1 barekube install gvisor "$flip" >/dev/null
  wait_for 180 "a pod without a RuntimeClass gets '${flipped:-none}' after $flip" plain_pod_gets_class "$flipped" || return 1
  node master-1 barekube install gvisor "$restore" >/dev/null
  wait_for 180 "a pod without a RuntimeClass gets '${original:-none}' after $restore" plain_pod_gets_class "$original" || return 1
}

check_an_agent_refuses_the_gvisor_default() {
  ! node agent-1 barekube install gvisor --default >/dev/null 2>&1 || { log "an agent accepted --default"; return 1; }
}

runsc_runtime_stanzas() {
  node "$1" sh -c "grep -c 'containerd.runtimes.runsc\\]' /var/lib/rancher/k3s/agent/etc/containerd/config.toml.tmpl"
}

check_gvisor_is_installed_on_every_node() {
  for n in "${NODES[@]}"; do
    node "$n" runsc --version >/dev/null || { log "runsc is missing on $n"; return 1; }
    node "$n" sh -c 'command -v containerd-shim-runsc-v1' >/dev/null || { log "the runsc containerd shim is missing on $n"; return 1; }
    expect_eq "$(runsc_runtime_stanzas "$n")" 1 "runsc runtime registered in the k3s containerd template on $n" || return 1
  done
}

check_gvisor_install_command_can_be_rerun() {
  node agent-1 barekube install gvisor >/dev/null
  node master-2 barekube install gvisor >/dev/null
  for n in agent-1 master-2; do
    expect_eq "$(runsc_runtime_stanzas "$n")" 1 "runsc runtime stanzas on $n after re-running" || return 1
    wait_for 240 "$n is Ready after re-running" node_ready "$n" || return 1
  done
}

# ------------------------------------------------------ Longhorn prerequisites

check_longhorn_prerequisites() {
  for n in "${NODES[@]}"; do
    node "$n" dpkg -s open-iscsi nfs-common curl grep bash >/dev/null || { log "$n lacks Longhorn packages"; return 1; }
    node "$n" iscsiadm --version >/dev/null || { log "iscsiadm is missing on $n"; return 1; }
    node "$n" systemctl is-active iscsid >/dev/null || {
      log "iscsid is not running on $n"
      node "$n" systemctl --no-pager status iscsid iscsid.socket 2>&1 | head -20 || true
      return 1
    }
  done
}

check_longhorn_install_command_can_be_rerun() {
  node agent-1 barekube install longhorn-dependencies >/dev/null
  node agent-1 systemctl is-active iscsid >/dev/null
}

# ----------------------------------------------------- failures and day two

# Only for clusters built by the playbook: running it again must change nothing.
check_playbook_rerun_changes_nothing() {
  local out
  out="$(run_playbook 2>&1)" || { printf '%s\n' "$out" | tail -60; return 1; }
  printf '%s\n' "$out" | grep -E "^(master|agent)-[0-9]+ +:" || true
  ! printf '%s\n' "$out" | grep -E "^(master|agent)-[0-9]+ +:" | grep -qvE "changed=0 +unreachable=0 +failed=0" \
    || { log "the playbook changed something on a second run"; return 1; }
}

# The playbook adds the cluster to ~/.kube/config as the current context, so
# kubectl works from the machine that ran it with no extra options.
check_kubectl_works_with_the_saved_kubeconfig() {
  [[ -s ~/.kube/config ]] || { log "the playbook saved no kubeconfig"; return 1; }
  expect_eq "$(kubectl config current-context)" "$KUBE_CONTEXT" "current context" || return 1
  expect_eq "$(kubectl get nodes --no-headers | wc -l)" 5 "nodes seen by kubectl" || return 1
}

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
  # Whatever happens below, master-1 is started again when the check ends: the
  # checks after this one talk to the cluster through master-1.
  trap 'node master-1 barekube k3s start >/dev/null 2>&1 || true' EXIT
  node master-1 barekube k3s stop
  # The control plane keeps quorum with two of three masters.
  wait_for 240 "master-1 is reported NotReady" master_1_not_ready_via_master_2 || return 1
  # Writes and scheduling still work through the remaining masters. The probe
  # has no spread constraint, so the missing node cannot hold it back.
  KUBE_NODE=master-2 kc create deployment ha-probe --image="$AGNHOST" --replicas=3 -- /agnhost pause >/dev/null
  KUBE_NODE=master-2 kc rollout status deploy/ha-probe --timeout=240s || return 1
  curl -fsS --max-time 10 "http://$(node_ip agent-1):30080/hostname" >/dev/null || { log "workloads stopped serving"; return 1; }
  node master-1 barekube k3s start
  trap - EXIT
  wait_for 300 "master-1 is Ready again" node_ready master-1 || return 1
  kc delete deployment ha-probe >/dev/null
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

# Day two with the playbooks: take a node out and bring it back by running
# site.yml again. A master also has to leave etcd.
etcd_members() { kc get nodes -l node-role.kubernetes.io/etcd=true --no-headers | wc -l; }

check_playbook_removes_and_readds_an_agent() {
  run_playbook remove_node.yml -e barekube_node=agent-2 >"$STATE_DIR/logs/remove-agent.log" 2>&1 \
    || { tail -40 "$STATE_DIR/logs/remove-agent.log"; return 1; }
  expect_eq "$(kc get nodes --no-headers | wc -l)" 4 "nodes after removing agent-2" || return 1
  ! node agent-2 test -e /usr/local/bin/k3s || { log "k3s is still installed on agent-2"; return 1; }
  # The node is gone from the inventory as well: the playbook leaves the cluster alone.
  inventory_drop_host agent-2
  run_playbook >"$STATE_DIR/logs/drop-agent.log" 2>&1 || { tail -40 "$STATE_DIR/logs/drop-agent.log"; return 1; }
  expect_eq "$(kc get nodes --no-headers | wc -l)" 4 "nodes after the playbook ran without agent-2" || return 1
  inventory_add_host agents agent-2
  run_playbook >"$STATE_DIR/logs/readd-agent.log" 2>&1 || { tail -40 "$STATE_DIR/logs/readd-agent.log"; return 1; }
  wait_for 120 "agent-2 is Ready again" node_ready agent-2 || return 1
  expect_eq "$(kc get nodes --no-headers | wc -l)" 5 "nodes after adding agent-2 back"
}

check_playbook_removes_and_readds_a_master() {
  run_playbook remove_node.yml -e barekube_node=master-3 >"$STATE_DIR/logs/remove-master.log" 2>&1 \
    || { tail -40 "$STATE_DIR/logs/remove-master.log"; return 1; }
  expect_eq "$(kc get nodes --no-headers | wc -l)" 4 "nodes after removing master-3" || return 1
  wait_for 120 "master-3 has left etcd" test "$(etcd_members)" -eq 2 || return 1
  inventory_drop_host master-3
  run_playbook >"$STATE_DIR/logs/drop-master.log" 2>&1 || { tail -40 "$STATE_DIR/logs/drop-master.log"; return 1; }
  expect_eq "$(etcd_members)" 2 "etcd members after the playbook ran without master-3" || return 1
  inventory_add_host masters master-3
  run_playbook >"$STATE_DIR/logs/readd-master.log" 2>&1 || { tail -40 "$STATE_DIR/logs/readd-master.log"; return 1; }
  wait_for 180 "master-3 is Ready again" node_ready master-3 || return 1
  wait_for 120 "master-3 is in etcd again" test "$(etcd_members)" -eq 3
}

check_playbook_refuses_to_remove_the_first_master() {
  ! run_playbook remove_node.yml -e barekube_node=master-1 >/dev/null 2>&1 || { log "the first master was removed"; return 1; }
  node_ready master-1
}

check_playbook_uninstall_needs_confirmation_and_then_removes_k3s() {
  ! run_playbook uninstall.yml >/dev/null 2>&1 || { log "uninstall ran without confirmation"; return 1; }
  node_ready master-1 || { log "the cluster went down without confirmation"; return 1; }
  run_playbook uninstall.yml -e barekube_confirm_uninstall=yes >"$STATE_DIR/logs/uninstall.log" 2>&1 \
    || { tail -40 "$STATE_DIR/logs/uninstall.log"; return 1; }
  for n in "${NODES[@]}"; do
    ! node "$n" test -e /usr/local/bin/k3s || { log "k3s is still installed on $n"; return 1; }
  done
  ! kubectl config get-contexts -o name | grep -qx "$KUBE_CONTEXT" \
    || { log "the cluster is still in the kubeconfig"; return 1; }
}


# ------------------------------------------ updating a cluster by its inventory
# The inventory is the desired state: change it, run the playbook, and the
# running cluster follows. Each step runs the playbook again on the same cluster.

run_playbook_logged() {
  local log_file="$STATE_DIR/logs/$1.log"
  run_playbook >"$log_file" 2>&1 || { tail -60 "$log_file"; return 1; }
}

deployment_is_absent() { ! kc -n kube-system get deploy "$1" >/dev/null 2>&1; }
deployment_exists() { kc -n kube-system get deploy "$1" >/dev/null 2>&1; }
servicelb_is_absent() { [[ "$(kc -n kube-system get ds -o name)" != *svclb* ]]; }

all_nodes_are_ready() {
  local n
  for n in "${NODES[@]}"; do node_ready "$n" || return 1; done
}

# The Subject Alternative Names of the certificate a master serves on 6443.
api_certificate_names() {
  local ip
  ip="$(node_ip "$1")"
  echo | openssl s_client -connect "$ip:6443" 2>/dev/null | openssl x509 -noout -ext subjectAltName
}

check_update_turns_components_off() {
  deployment_exists traefik || { log "traefik is not there to be turned off"; return 1; }
  inventory_set barekube_disable "[servicelb, traefik]"
  run_playbook_logged update-disable || return 1
  wait_for 240 "traefik is gone" deployment_is_absent traefik || return 1
  wait_for 120 "servicelb is gone" servicelb_is_absent || return 1
  wait_for 300 "every node is Ready" all_nodes_are_ready
}

check_update_adds_a_tls_san() {
  inventory_set barekube_tls_san '["update.e2e.example"]'
  run_playbook_logged update-tls-san || return 1
  local m
  for m in "${MASTERS[@]}"; do
    wait_for 180 "$m serves a certificate for update.e2e.example" \
      bash -c "$(declare -f node_ip node api_certificate_names); api_certificate_names $m | grep -q update.e2e.example" \
      || return 1
  done
}

check_update_turns_a_component_back_on() {
  inventory_set barekube_disable "[servicelb]"
  run_playbook_logged update-enable || return 1
  wait_for 300 "traefik is back" deployment_exists traefik || return 1
  wait_for 300 "every node is Ready" all_nodes_are_ready
}

check_update_passes_extra_arguments() {
  inventory_set barekube_extra_args '"--node-label=e2e-update=true"'
  run_playbook_logged update-extra-args || return 1
  wait_for 300 "every node is Ready" all_nodes_are_ready || return 1
  # Only servers take the arguments; the agents have no such label.
  expect_eq "$(kc get nodes -l e2e-update=true --no-headers | wc -l)" 3 "nodes with the label" || return 1
}

check_update_flips_the_gvisor_default() {
  local flipped="true" want=gvisor
  [[ "$GVISOR_DEFAULT" == "true" ]] && { flipped="false"; want=""; }
  inventory_set barekube_gvisor_default "$flipped"
  run_playbook_logged update-gvisor || return 1
  wait_for 240 "a pod without a RuntimeClass gets '${want:-none}'" plain_pod_gets_class "$want" || return 1
  inventory_set barekube_gvisor_default "$GVISOR_DEFAULT"
  run_playbook_logged update-gvisor-back || return 1
  want=""; [[ "$GVISOR_DEFAULT" == "true" ]] && want=gvisor
  wait_for 240 "a pod without a RuntimeClass gets '${want:-none}' again" plain_pod_gets_class "$want"
}

# After all the changes the playbook has nothing left to do.
check_update_settles() {
  check_playbook_rerun_changes_nothing
}

# --------------------------------------------------------------------- runner

check "barekube is the version under test on every node" check_version
check "all five nodes are Ready" check_nodes_ready
check "the control plane is highly available (3 masters, 3 etcd members)" check_control_plane_is_highly_available
if components_disabled; then
  check "disabled components are not installed" check_disabled_components_are_absent
else
  check "components left on by the defaults are installed" check_default_components_are_present
fi
check "system workloads are healthy" check_system_workloads_are_healthy
check "every node holds a WireGuard mesh with the other four" check_wireguard_mesh
group "Deploy the echo workload"
apply_echo
endgroup
check "echo pods spread over all five nodes" check_pods_spread_over_every_node
check "pods reach each other across nodes" check_pod_to_pod_across_nodes
check "services and DNS resolve" check_service_and_dns
check "a NodePort answers on every node" check_nodeport_on_every_node
check "gVisor is installed and registered with k3s on every node" check_gvisor_is_installed_on_every_node
check "a pod runs sandboxed by gVisor" check_gvisor_runtime
check "the gvisor and runc RuntimeClasses exist" check_runtime_classes_exist
check "pods without a RuntimeClass follow the cluster's gVisor default ($GVISOR_DEFAULT)" check_default_follows_the_setting
check "system pods are left out of the gVisor default" check_system_pods_are_left_alone
check "pods that need the host are left out of the gVisor default" check_host_pods_are_left_alone
check "a runc RuntimeClass opts a pod out of gVisor" check_runc_runtime_class_opts_out
check "the gVisor default can be switched from a master, both ways" check_gvisor_default_can_be_switched
check "an agent refuses the gVisor default" check_an_agent_refuses_the_gvisor_default
check "Longhorn prerequisites are in place on every node" check_longhorn_prerequisites
check "re-running the gVisor install keeps the cluster healthy" check_gvisor_install_command_can_be_rerun
check "re-running the Longhorn dependencies install works" check_longhorn_install_command_can_be_rerun
check "re-running installs is idempotent" check_idempotent_install
if uses_playbooks; then
  check "kubectl works with the kubeconfig the playbook saved" check_kubectl_works_with_the_saved_kubeconfig
  check "running the playbook again changes nothing" check_playbook_rerun_changes_nothing
  check "the playbook refuses to remove the first master" check_playbook_refuses_to_remove_the_first_master
  check "the playbook removes an agent and site.yml adds it back" check_playbook_removes_and_readds_an_agent
  check "the playbook removes a master from etcd and site.yml adds it back" check_playbook_removes_and_readds_a_master
fi
if uses_playbooks; then
  # Update scenario: change the inventory, run the playbook, the cluster follows.
  if [[ "$INVENTORY_KIND" == "minimal" ]]; then
    check "update: components are turned off by the inventory" check_update_turns_components_off
  fi
  check "update: a TLS SAN added to the inventory reaches the API certificate" check_update_adds_a_tls_san
  check "update: a component is turned back on by the inventory" check_update_turns_a_component_back_on
  check "update: extra k3s arguments reach the servers only" check_update_passes_extra_arguments
  check "update: the gVisor default follows the inventory, both ways" check_update_flips_the_gvisor_default
  check "update: the playbook settles with nothing left to change" check_update_settles
fi
check "the cluster survives losing a master" check_cluster_survives_losing_a_master
check "an agent can be stopped and started" check_agent_stop_and_start
check "an agent can be uninstalled and removed" check_agent_uninstall
if uses_playbooks; then
  check "the uninstall playbook needs a confirmation, then removes k3s everywhere" check_playbook_uninstall_needs_confirmation_and_then_removes_k3s
fi

if ((${#FAILED[@]})); then
  log "${#FAILED[@]} check(s) failed:"
  printf '  - %s\n' "${FAILED[@]}"
  exit 1
fi
log "all checks passed"
