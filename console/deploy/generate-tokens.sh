#!/usr/bin/env bash
# Writes .env with the two secrets the console needs: the UI token and the node
# token. They are unrelated on purpose. A value already in the environment is
# used instead of a random one, for example the node token the playbook printed:
#   CONSOLE_NODE_TOKEN=<token> ./generate-tokens.sh Refuses to overwrite an existing .env,
# so tokens already given to the nodes are not replaced by accident.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
[[ ! -e .env ]] || { echo ".env exists; delete it first to make new tokens" >&2; exit 1; }
umask 077
{
  echo "CONSOLE_UI_TOKEN=${CONSOLE_UI_TOKEN:-$(openssl rand -hex 24)}"
  echo "CONSOLE_NODE_TOKEN=${CONSOLE_NODE_TOKEN:-$(openssl rand -hex 24)}"
} >.env
echo "Wrote $PWD/.env"
echo "Give the nodes the node token:  CONSOLE_NODE_TOKEN=\$(sed -n 's/^CONSOLE_NODE_TOKEN=//p' .env) barekube install node-agent ..."
echo "or set barekube_console_node_token to it in the Ansible inventory. Sign in to the UI with CONSOLE_UI_TOKEN."
