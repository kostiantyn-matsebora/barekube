#!/usr/bin/env bash
# Builds the five-node cluster (3 masters, 2 agents) with the barekube script
# of this checkout, exactly as the README and the orchestration plan describe:
# dependencies everywhere, the initial master, the other masters one at a
# time, then the agents - all non-interactively.
set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

launch_node() {
  local name="$1"
  lxc launch ubuntu:24.04 "$name" -p default -p barekube-e2e
  wait_for 120 "$name is running" node "$name" true
  node "$name" cloud-init status --wait || true
  # Nothing else may hold the apt lock while barekube installs packages.
  node "$name" systemctl disable --now unattended-upgrades apt-daily.timer apt-daily-upgrade.timer || true
  node "$name" bash -c 'mkdir -p /etc/needrestart/conf.d && echo "\$nrconf{restart} = \"a\";" >/etc/needrestart/conf.d/90-auto.conf'
  # The kubelet reads /dev/kmsg, which a container does not have.
  node "$name" bash -c 'echo "L /dev/kmsg - - - - /dev/console" >/etc/tmpfiles.d/kmsg.conf && ln -sf /dev/console /dev/kmsg'
  # With the Ansible provisioner the playbook puts the CLI on the nodes itself.
  if [[ "$PROVISIONER" == "script" ]]; then
    lxc file push "$ROOT/cli/release/barekube" "$name/usr/local/bin/barekube" --mode 0755
    node "$name" barekube --version
  fi
}

install_prerequisites() {
  node "$1" barekube install dependencies
  node "$1" barekube install longhorn-dependencies
}

# install_ok NODE COMMAND...: runs a barekube install and requires the plain
# "installed" line that tools driving the CLI rely on.
install_ok() {
  local name="$1" out
  shift
  out="$("$@" 2>&1)" || { printf '%s\n' "$out"; fail "$* failed on $name"; }
  printf '%s\n' "$out" | sed 's/\x1b\[[0-9;]*m//g'
  [[ "$(printf '%s\n' "$out" | last_line)" == "installed" ]] || fail "$name: expected the last output line to be 'installed'"
}

join_agent() {
  install_ok "$1" lxc exec "$1" --env K3S_TOKEN="$TOKEN" -- \
    barekube install agent-node --yes --server "https://$FIRST_IP:6443"
}

log "gVisor is the default for pods: $GVISOR_DEFAULT"
group "Create the five nodes"
parallel_nodes launch_node "${NODES[@]}"
endgroup

# The Ansible provisioner builds the cluster with the playbook in
# orchestration/ansible and the CLI of this checkout; everything after the
# nodes exist is the playbook's work.
if [[ "$PROVISIONER" == "ansible" ]]; then
  group "Build the cluster with the Ansible playbook"
  FIRST_IP="$(node_ip master-1)"

  # The playbooks, as the release archive a user downloads.
  rm -rf "$STATE_DIR/pack" "$PLAYBOOK_DIR"
  "$ROOT/orchestration/package.sh" "$STATE_DIR/pack"
  tar -xzf "$STATE_DIR/pack/barekube-ansible.tar.gz" -C "$STATE_DIR"
  cp "$ROOT/cli/release/barekube" "$PLAYBOOK_DIR/barekube"

  # The nodes are reached over SSH as the ubuntu user with sudo, like real machines.
  mkdir -p ~/.ssh && chmod 700 ~/.ssh
  [[ -f ~/.ssh/id_ed25519 ]] || ssh-keygen -q -t ed25519 -N '' -f ~/.ssh/id_ed25519
  for n in "${NODES[@]}"; do
    wait_for 120 "sshd is running on $n" node "$n" systemctl is-active ssh
    node "$n" install -d -m 700 -o ubuntu -g ubuntu /home/ubuntu/.ssh
    lxc file push ~/.ssh/id_ed25519.pub "$n/home/ubuntu/.ssh/authorized_keys" --uid 1000 --gid 1000 --mode 600
  done

  {
    echo "all:"
    echo "  children:"
    echo "    masters:"
    echo "      hosts:"
    for n in "${MASTERS[@]}"; do echo "        $n: { ansible_host: $(node_ip "$n") }"; done
    echo "    agents:"
    echo "      hosts:"
    for n in "${AGENTS[@]}"; do echo "        $n: { ansible_host: $(node_ip "$n") }"; done
    echo "  vars:"
    echo "    ansible_user: ubuntu"
    echo "    ansible_become: true"
    echo "    ansible_ssh_common_args: -o StrictHostKeyChecking=accept-new"
  } >"$PLAYBOOK_DIR/inventory.yml"
  cat "$PLAYBOOK_DIR/inventory.yml"
  run_playbook
  TOKEN="$(node master-1 cat /var/lib/rancher/k3s/server/token)"
  echo "::add-mask::$TOKEN"
  printf '%s\n' "$FIRST_IP" >"$STATE_DIR/first-ip"
  printf '%s' "$TOKEN" >"$STATE_DIR/token"
  chmod 600 "$STATE_DIR/token"
  kc get nodes -o wide
  endgroup
  log "cluster is up"
  exit 0
fi

group "Install dependencies on every node"
parallel_nodes install_prerequisites "${NODES[@]}"
endgroup

group "Initial master"
FIRST_IP="$(node_ip master-1)"
log "master-1 is $FIRST_IP"
install_ok master-1 lxc exec master-1 -- \
  barekube install initial-master --yes --tls-san "$FIRST_IP" "${K3S_FLAGS[@]}" "${GVISOR_FLAGS[@]}"
TOKEN="$(node master-1 cat /var/lib/rancher/k3s/server/token)"
echo "::add-mask::$TOKEN"
wait_for 300 "master-1 is Ready" node_ready master-1
endgroup

# etcd members must join one at a time.
for master in master-2 master-3; do
  group "Join $master"
  install_ok "$master" lxc exec "$master" --env K3S_TOKEN="$TOKEN" -- \
    barekube install master-node --yes --server "https://$FIRST_IP:6443" \
    --tls-san "$FIRST_IP" "${K3S_FLAGS[@]}" "${GVISOR_FLAGS[@]}"
  wait_for 300 "$master is Ready" node_ready "$master"
  endgroup
done

group "Join the agents"
export TOKEN FIRST_IP
parallel_nodes join_agent "${AGENTS[@]}"
for agent in "${AGENTS[@]}"; do
  wait_for 300 "$agent is Ready" node_ready "$agent"
done
endgroup

printf '%s\n' "$FIRST_IP" >"$STATE_DIR/first-ip"
printf '%s' "$TOKEN" >"$STATE_DIR/token"
chmod 600 "$STATE_DIR/token"
kc get nodes -o wide
log "cluster is up"
