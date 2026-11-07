#!/usr/bin/env bash
# Runs ansible-playbook. Uses the Ansible on your machine if there is one,
# otherwise a Docker image built from ./Dockerfile.
#
#   ./play.sh                                  build the cluster in inventory.yml
#   ./play.sh remove-node.yml -e barekube_node=agent-2
#
# Without -i it reads inventory.yml, and without a playbook it runs site.yml.
# Anything else is passed on to ansible-playbook.
#
# Set BAREKUBE_USE_DOCKER=1 to use Docker even when Ansible is installed.
#
# Keep the inventory in this directory (inventory.yml is ignored by git). Your
# SSH agent or ~/.ssh keys are used to reach the nodes, and the kubeconfig is
# added to ~/.kube/config as usual.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Fill in the inventory and the playbook when they are not given.
has_inventory=""
has_playbook=""
skip=""
for arg in "$@"; do
  if [[ -n "$skip" ]]; then skip=""; continue; fi
  case "$arg" in
    -i | --inventory | -e | --extra-vars | -l | --limit | -t | --tags) skip=1 ;;&
    -i | --inventory | --inventory=*) has_inventory=1 ;;
    *.yml | *.yaml) has_playbook=1 ;;
  esac
done
set -- "$@"
[[ -n "$has_inventory" ]] || set -- -i inventory.yml "$@"
[[ -n "$has_playbook" ]] || set -- "$@" site.yml

if [[ -z "${BAREKUBE_USE_DOCKER:-}" ]] && command -v ansible-playbook >/dev/null 2>&1; then
  cd "$here"
  exec ansible-playbook "$@"
fi

command -v docker >/dev/null 2>&1 || {
  echo "Install Ansible (pipx install 'ansible-core>=2.18,<2.22') or Docker to run the playbooks." >&2
  exit 1
}

docker build --quiet --tag barekube-ansible "$here" >/dev/null
mkdir -p "$HOME/.kube"

args=(
  --rm --interactive
  --network host
  --user "$(id -u):$(id -g)"
  --env HOME=/home/barekube
  --env ANSIBLE_HOME=/tmp/ansible-home
  --env ANSIBLE_LOCAL_TEMP=/tmp/ansible-tmp
  --volume "$here:/ansible"
  --volume "$HOME/.kube:/home/barekube/.kube"
)
[[ -t 0 ]] && args+=(--tty)
[[ -d "$HOME/.ssh" ]] && args+=(--volume "$HOME/.ssh:/home/barekube/.ssh:ro")
if [[ -n "${SSH_AUTH_SOCK:-}" && -S "${SSH_AUTH_SOCK}" ]]; then
  args+=(--volume "$SSH_AUTH_SOCK:/ssh-agent" --env SSH_AUTH_SOCK=/ssh-agent)
fi
exec docker run "${args[@]}" barekube-ansible "$@"
