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

Add to the inventory, in `all: vars:` (or on a group or a host, to override):

```yaml
all:
  vars:
    barekube_console_agent: true                       # default false
    barekube_console_address: http://192.168.1.5:8080  # where the console will run
    barekube_console_export_dir: console/deploy/config # optional: writes the console configuration (step 2)
    # barekube_console_node_token: ...                 # optional: choose the node token yourself
```

Run the playbook as for the cluster (`site.yml`). It downloads and verifies the node-agent (checksum and
Sigstore signature), installs it on every node that has `barekube_console_agent: true`, and at the end
**prints the node token** it made. Write it down: step 2 needs it. Run again and it reads the token back from
the agents, so it stays the same and is not printed again. To choose it yourself, set
`barekube_console_node_token` (16 or more characters of `A-Z a-z 0-9 . _ ~ + / = -`, best from Ansible Vault).

Everything per node has a default, so nothing else is needed. The role comes from the group a host is in
(`masters` or `agents`). To override, put the variable on a host, a group or `all`; the most specific wins:

```yaml
all:
  children:
    masters:
      hosts:
        192.168.1.10:
        192.168.1.15:
          barekube_console_port: 9199              # only this node's agent listens here
          barekube_console_operations: [reboot, standby]   # and may only do these
    agents:
      hosts:
        192.168.1.50:
        192.168.1.30:
          barekube_console_agent: false            # no agent on this node
      vars:
        barekube_console_operations: [reboot, standby, rejoin]
```

| Variable | Default | |
|---|---|---|
| `barekube_console_agent` | `false` | install the agent on the node |
| `barekube_console_address` | required | the console's address, where the nodes send heartbeats |
| `barekube_console_node_token` | made for you | the shared secret |
| `barekube_console_node_name` | the inventory name | the node's name in the console |
| `barekube_console_port` | `9091` | the port of the agent |
| `barekube_console_operations` | by role | any of `reboot`, `standby`, `rejoin`, `poweroff`. Masters: all four. Agents: without `poweroff` |
| `barekube_console_heartbeat_interval` | `5s` | how often the node reports |
| `barekube_console_etcd_metrics_url` | the agent's default | masters: local etcd metrics, for the leader probe |
| `barekube_console_api_vip`, `barekube_console_api_gateway` | empty | masters: enable the cold-start helper, see [Platform](reference.md#platform-cilium-kube-vip-the-api-vip) |
| `barekube_console_export_dir` | empty | where to write the console configuration |

The whole list, with examples: [INVENTORY.md](../../orchestration/ansible/INVENTORY.md#the-console-agents). To take the
agents off again: `ansible-playbook -i inventory.yml remove_console_agent.yml`.

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

