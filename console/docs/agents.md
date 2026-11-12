# Installing the node-agents and the console

The full guide behind the short one in the [README](../README.md#install).

Three parts: the **console** (a Docker container on one machine), the **node-agent** on every
node, and the **configuration** that connects them. Nothing is needed to run a cluster without it.

```
 browser ──► console (Docker) ──HTTPS to :9091, pinned certificate, token──► node-agent on every node
                  ▲                                                               │
                  └───────────────── heartbeat to :8080, same token ──────────────┘
```

**Before you start**

- A cluster built by barekube (or any k3s cluster on Ubuntu or Debian nodes with systemd).
- A machine that stays up when the cluster is off, with Docker and Docker Compose. It is the machine
  that can start a cluster again, so not a node of the cluster.
- Network: the console reaches every node on the agent port (9091 by default), and every node
  reaches the console on its port (8080 by default).

## 1. The node-agents

Every node needs one. It is a small daemon that runs as an account that cannot log in and that does
only a fixed list of things (see [What a node-agent is allowed](#what-a-node-agent-is-allowed)).
Pick Ansible (the same inventory that builds the cluster) or the CLI (one node at a time).

All nodes share one secret, the **node token**. The console needs the same value as
`CONSOLE_NODE_TOKEN`.

### With Ansible

Set `barekube_console_agent: true` and `barekube_console_address` in the inventory and run the playbook; it prints the node token.
Every setting, per-node overrides and examples: [Console agents in the Ansible README](../../orchestration/ansible/README.md#console-agents-optional).
To take the agents off: `ansible-playbook -i inventory.yml remove_console_agent.yml`.

### With the CLI

On each node (the CLI is installed by the [README](../../README.md#using-the-cli)):

```sh
CONSOLE_NODE_TOKEN='<the node token>' barekube install node-agent --yes \
  --console-addr http://192.168.1.5:8080 --node-name master-1
```

Make the token with `openssl rand -hex 24`, and use the same one on every node. The options, each of
which has a default:

| Option | Default | |
|---|---|---|
| `--console-addr` | required | the console's address |
| `--node-name` | the host name | the node's name in the console |
| `--port` | `9091` | the agent's port |
| `--operations` | by role | for example `reboot,standby,rejoin`; the node's permissions follow it |
| `--heartbeat-interval` | `5s` | |
| `--etcd-metrics-url` | the agent's default | masters only |
| `--api-vip` and `--api-gateway` | none | masters only, together |
| `--binary` | the release | install this file instead |
| `--remove` | | take the agent off the node |

It prints `fingerprint <hex>` (the pinned certificate, not a secret) and ends with `installed` or
`already installed`. Run it again with other options and only what differs changes.

## 2. The configuration

The console is told its nodes in `config/config.yaml`, and trusts a node only if its agent certificate has
the fingerprint in `config/credentials/<node name>`.

- With Ansible and `barekube_console_export_dir`, both are written for you from the inventory. Nothing to do.
- By hand: copy [`config.example.yaml`](../config.example.yaml) to `config/config.yaml` and list every node
  (`name`, `ip`, `role`, `nodeAgentPort`, `operations`: the same values as `--node-name`, `--port` and
  `--operations`), and save each node's fingerprint, as printed by the CLI, as `config/credentials/<node name>`.

`config/` goes next to the compose file, in `console/deploy/`.

## 3. The console

```sh
cd console/deploy          # from a checkout, or just the files of this directory
CONSOLE_NODE_TOKEN='<the node token>' ./generate-tokens.sh   # writes .env, with a random UI token
docker compose up -d
```

Two secrets, unrelated on purpose, are read from `.env` (or the environment):

| Variable | Opens |
|---|---|
| `CONSOLE_UI_TOKEN` | the web UI. Sign in with it at `http://<machine>:8080` |
| `CONSOLE_NODE_TOKEN` | the agents' door, the token from step 1 |

`generate-tokens.sh` only fills `.env` in (with a random value for any token you did not give), so you can
write the file yourself. The compose file refuses to start without both.

The image is `ghcr.io/kostiantyn-matsebora/barekube-console`, published with every release for amd64, arm64
and arm/v7 and signed with Sigstore. Set `CONSOLE_VERSION` in `.env` to pin a release (default `latest`).
The state is kept in the `console-data` volume. The UI has no TLS of its own: put it behind your own proxy or
keep it on a trusted network.

**Check it:** `console/deploy/check-running.sh http://localhost:8080 <UI token> <node token>` tests that each
token opens only its own door. The node table in the UI should show every node as reachable within seconds.

**Upgrade:** set the new `CONSOLE_VERSION` and `docker compose pull && docker compose up -d`; upgrade the agents by running the
playbook, or `barekube install node-agent`, with the new release.

## What a node-agent is allowed

The agent runs as the system account `barekube-agent`, which cannot log in and has no
sudo. A polkit rule lets it reboot, start/stop/kill the one k3s unit of its node,
and, on masters, power off. `--operations` narrows that list: an operation left out
is not granted. The one privileged command that is no polkit action, the local etcd
snapshot of a cluster shutdown, has a sudoers line for that exact command. The token
is in a file only root reads. The agent exposes fixed endpoints and never a generic "run this command".

