#!/usr/bin/env bash
# Runs ansible-playbook. Uses the Ansible on your machine if there is one,
# otherwise a Docker image built from ./Dockerfile.
#
#   ./play.sh                                   build the cluster in ./inventory.yml
#   ./play.sh -i ~/clusters/home/inventory.yml  use an inventory anywhere
#   ./play.sh remove_node.yml -e barekube_node=agent-2
#
# Without -i it reads inventory.yml in the directory you run it from (or, if
# there is none, the one next to this script). Without a playbook it runs
# site.yml. Anything else is passed on to ansible-playbook.
#
# Log in with a password instead of a key: ./play.sh -k (and -K if sudo asks
# for one).
#
# Set BAREKUBE_USE_DOCKER=1 to use Docker even when Ansible is installed. Your
# SSH agent or ~/.ssh keys are used to reach the nodes, and the cluster is
# added to ~/.kube/config as usual.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
invoked_from="$PWD"

# The inventory as a path that works from anywhere. A value that is not a file
# or directory (a host list such as "node1,node2,") is left as it is.
absolute_inventory() {
  local value="$1"
  if [[ -e "$value" ]]; then
    printf '%s/%s\n' "$(cd "$(dirname "$value")" && pwd)" "$(basename "$value")"
  else
    printf '%s\n' "$value"
  fi
}

# Sort the arguments: the inventories (made absolute), the playbook, the rest.
inventories=()
rest=()
has_playbook=""
while (($#)); do
  case "$1" in
    -i | --inventory)
      shift
      [[ $# -gt 0 ]] || { echo "-i needs an inventory" >&2; exit 2; }
      inventories+=("$(absolute_inventory "$1")") ;;
    --inventory=*) inventories+=("$(absolute_inventory "${1#--inventory=}")") ;;
    -e | --extra-vars | -l | --limit | -t | --tags | -u | --user | --private-key)
      rest+=("$1")
      shift
      [[ $# -gt 0 ]] || break
      rest+=("$1") ;;
    *.yml | *.yaml)
      has_playbook=1
      # The playbooks live in playbooks/; they can be named without it.
      if [[ ! -e "$1" && -e "$here/playbooks/$1" ]]; then rest+=("playbooks/$1"); else rest+=("$1"); fi ;;
    *) rest+=("$1") ;;
  esac
  shift
done

if ((${#inventories[@]} == 0)); then
  if [[ -f "$invoked_from/inventory.yml" ]]; then
    inventories=("$invoked_from/inventory.yml")
  elif [[ -f "$here/inventory.yml" ]]; then
    inventories=("$here/inventory.yml")
  else
    echo "No inventory. Copy inventory.example.yml to inventory.yml and put your machines in it, or pass -i PATH." >&2
    exit 1
  fi
fi
[[ -n "$has_playbook" ]] || rest+=(playbooks/site.yml)

if [[ -z "${BAREKUBE_USE_DOCKER:-}" ]] && command -v ansible-playbook >/dev/null 2>&1; then
  args=()
  for inventory in "${inventories[@]}"; do args+=(-i "$inventory"); done
  cd "$here"
  exec ansible-playbook "${args[@]}" "${rest[@]}"
fi

command -v docker >/dev/null 2>&1 || {
  echo "Install Ansible (pipx install 'ansible-core>=2.18,<2.22') or Docker to run the playbooks." >&2
  exit 1
}

docker build --quiet --tag barekube-ansible "$here" >/dev/null
mkdir -p "$HOME/.kube"

docker_args=(
  --rm --interactive
  --network host
  --user "$(id -u):$(id -g)"
  --env HOME=/home/barekube
  --env ANSIBLE_HOME=/tmp/ansible-home
  --env ANSIBLE_LOCAL_TEMP=/tmp/ansible-tmp
  --volume "$here:/ansible"
  --volume "$HOME/.kube:/home/barekube/.kube"
)
[[ -t 0 ]] && docker_args+=(--tty)
if [[ -d "$HOME/.ssh" ]]; then
  # Keys are read-only; known_hosts is writable so a host is only asked about once.
  touch "$HOME/.ssh/known_hosts"
  docker_args+=(--volume "$HOME/.ssh:/home/barekube/.ssh:ro" --volume "$HOME/.ssh/known_hosts:/home/barekube/.ssh/known_hosts")
fi
if [[ -n "${SSH_AUTH_SOCK:-}" && -S "${SSH_AUTH_SOCK}" ]]; then
  docker_args+=(--volume "$SSH_AUTH_SOCK:/ssh-agent" --env SSH_AUTH_SOCK=/ssh-agent)
fi

# An inventory is mounted from where it is: its directory, so group_vars and
# host_vars next to it come along. One next to this script is already there.
args=()
n=0
for inventory in "${inventories[@]}"; do
  if [[ -e "$inventory" ]]; then
    if [[ "$(dirname "$inventory")" == "$here" ]]; then
      inventory="/ansible/$(basename "$inventory")"
    else
      n=$((n + 1))
      docker_args+=(--volume "$(dirname "$inventory"):/inventory/$n:ro")
      inventory="/inventory/$n/$(basename "$inventory")"
    fi
  fi
  args+=(-i "$inventory")
done
exec docker run "${docker_args[@]}" barekube-ansible "${args[@]}" "${rest[@]}"
