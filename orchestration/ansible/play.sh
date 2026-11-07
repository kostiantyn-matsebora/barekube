#!/usr/bin/env bash
# Runs ansible-playbook with the arguments you give it. Uses the Ansible on your
# machine if there is one, otherwise a Docker image built from ./Dockerfile.
#
#   ./play.sh -i inventory.yml site.yml
#
# Set BAREKUBE_USE_DOCKER=1 to use Docker even when Ansible is installed.
#
# Keep the inventory in this directory (inventory.yml is ignored by git). Your
# SSH agent or ~/.ssh keys are used to reach the nodes, and the kubeconfig is
# saved to ~/.barekube as usual.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ -z "${BAREKUBE_USE_DOCKER:-}" ]] && command -v ansible-playbook >/dev/null 2>&1; then
  cd "$here"
  exec ansible-playbook "$@"
fi

command -v docker >/dev/null 2>&1 || {
  echo "Install Ansible (pipx install 'ansible-core>=2.18,<2.22') or Docker to run the playbooks." >&2
  exit 1
}

docker build --quiet --tag barekube-ansible "$here" >/dev/null
mkdir -p "$HOME/.barekube"

args=(
  --rm --interactive
  --network host
  --user "$(id -u):$(id -g)"
  --env HOME=/home/barekube
  --env ANSIBLE_HOME=/tmp/ansible-home
  --env ANSIBLE_LOCAL_TEMP=/tmp/ansible-tmp
  --volume "$here:/ansible"
  --volume "$HOME/.barekube:/home/barekube/.barekube"
)
[[ -t 0 ]] && args+=(--tty)
[[ -d "$HOME/.ssh" ]] && args+=(--volume "$HOME/.ssh:/home/barekube/.ssh:ro")
if [[ -n "${SSH_AUTH_SOCK:-}" && -S "${SSH_AUTH_SOCK}" ]]; then
  args+=(--volume "$SSH_AUTH_SOCK:/ssh-agent" --env SSH_AUTH_SOCK=/ssh-agent)
fi
exec docker run "${args[@]}" barekube-ansible "$@"
