# console

The optional control plane of a barekube cluster, with a web UI. It runs from a Docker
image on any machine that outlives the cluster, and manages the nodes through a small
**node-agent** that barekube installs on each of them.
Its own web UI is the only way in, apart from one narrow automation route.

Nothing here is needed to run a cluster. Barekube builds and runs the cluster without it.

Every operation is a **workflow** that moves only on what it observes. Nothing is
assumed. A reboot is not "joining" because a timer ran out. It is "joining" because
the node was seen unreachable and then reachable and Ready again.

## Install

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

### 1. Install the node-agents

Every node needs one. It is a small daemon that runs as an account that cannot log in and that does
only a fixed list of things (see [What a node-agent is allowed](#what-a-node-agent-is-allowed)).
Pick Ansible (the same inventory that builds the cluster) or the CLI (one node at a time).

All nodes share one secret, the **node token**. The console needs the same value as
`CONSOLE_NODE_TOKEN`.

#### With Ansible

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
| `barekube_console_api_vip`, `barekube_console_api_gateway` | empty | masters: enable the cold-start helper, see [Platform](#platform-cilium-kube-vip-the-api-vip) |
| `barekube_console_export_dir` | empty | where to write the console configuration |

The whole list, with examples: [INVENTORY.md](../orchestration/ansible/INVENTORY.md#the-console-agents). To take the
agents off again: `ansible-playbook -i inventory.yml remove_console_agent.yml`.

#### With the CLI

On each node (the CLI is installed by the [README](../README.md#using-the-cli)):

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

### 2. Configure the console

The console is told its nodes in `config/config.yaml`, and trusts a node only if its agent certificate has
the fingerprint in `config/credentials/<node name>`.

- With Ansible and `barekube_console_export_dir`, both are written for you from the inventory. Nothing to do.
- By hand: copy [`config.example.yaml`](config.example.yaml) to `config/config.yaml` and list every node
  (`name`, `ip`, `role`, `nodeAgentPort`, `operations`: the same values as `--node-name`, `--port` and
  `--operations`), and save each node's fingerprint, as printed by the CLI, as `config/credentials/<node name>`.

`config/` goes next to the compose file, in `console/deploy/`.

### 3. Start the console with Docker Compose

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

### What a node-agent is allowed

The agent runs as the system account `barekube-agent`, which cannot log in and has no
sudo. A polkit rule lets it reboot, start/stop/kill the one k3s unit of its node,
and, on masters, power off. `--operations` narrows that list: an operation left out
is not granted. The one privileged command that is no polkit action, the local etcd
snapshot of a cluster shutdown, has a sudoers line for that exact command. The token
is in a file only root reads. The agent exposes fixed endpoints and never a generic "run this command".

## What it does

| Operation | What happens |
|---|---|
| Reboot | cordon, drain, reboot, wait until the node is seen down and back, uncordon |
| Stand by | cordon, drain, stop (or kill) k3s, wait until k3s is seen inactive |
| Power off | stand by, then poweroff, then `poweroff --force` if the graceful one is too slow |
| Rejoin | start k3s, wait until it is active and the node is Ready, uncordon |
| Cluster shutdown | agents stand by, masters power off one at a time, the etcd leader last |
| Cluster start | after the owner powers the nodes on: wait for the masters and an etcd majority, rejoin masters one at a time, then the agents, then report Longhorn, Vault and (read-only) the platform layer |
| Restore API VIP | **platform level**, on its own: break the Cilium / kube-vip API VIP deadlock if (and only if) it is present |

Each of these is a workflow in `internal/flows`.
Each has a diagram, drawn from the same declaration that runs it,
and shown live in the UI for the selected run.

The console runs **one root workflow at a time**.
The cluster shutdown and the cluster start start their own sub-workflows, and those are exempt.

### Cluster start (the nodes)

For a cluster that is fully off. The console cannot power nodes on, so the owner does,
and the console takes over from there. It is started from the UI only
(`POST /api/cluster/start`, session login): not by the automation token, since Home
Assistant runs in the cluster and is not up to ask.

It is about the **nodes only**: it never touches Cilium, kube-vip or the VIP. Its last step
reports; degraded Longhorn volumes, a sealed Vault and an unhealthy platform layer are notes
on a completed run ("run Restore API VIP"). A faulty volume, or a node that did not rejoin,
fails it. The console never unseals Vault.

### Platform (Cilium, kube-vip, the API VIP)

A node can be Ready while the layer on it is down, so the platform has its own tab, its own
state (`ok`, `partial`, `vip-down`, `down`, `deadlock`) and its own workflow. The tab is
shown only when Cilium is installed. The status bar always shows two pills, **cluster**
(nodes healthy / total) and **platform** (its state), each linking to its tab.

The card lists Cilium, kube-vip and the k3s add-ons (CoreDNS, Traefik, metrics-server,
local-path-provisioner, servicelb). The add-ons are optional, so each is read as present or
not: one that is not installed (`--disable`d, or replaced by something else, as CoreDNS is
here) shows as **off / not installed** and counts for nothing; one scaled to 0 is off too.
An installed one with fewer ready replicas than desired makes the platform `partial`.
servicelb is only visible through its per-Service `svclb-*` DaemonSets, so "not installed"
there also means "no LoadBalancer Service uses it". flannel, kube-proxy, the network-policy
controller and the helm- and cloud-controllers run inside the k3s process and have no pod to read.

Read: `GET /api/platform` (cached 5 s). Repair: `POST /api/platform/vip-restore`. Both are
session-only, like the cluster start.

After a full power-off the API VIP can deadlock: Cilium reaches the API through the VIP,
kube-vip needs Cilium to win the lease that announces it. `vip-restore` acts only when all of
this holds, and ends "nothing to restore" otherwise:

- `platform.vip` is configured,
- Cilium's API host is that VIP,
- no Cilium agent is ready and the VIP does not answer,
- and that has held for `platform.deadlockGrace`.

Then it starts the node's fixed `vip-bootstrap.service` on **one** master, waits until Cilium
is up and the kube-vip lease has been renewed since (a leftover lease proves nothing),
and removes the address again, unless kube-vip leads from that same master.
If the VIP stops answering without the address, the address is put back and the run fails.

Design and the diagrams: `docs/workflows.md`.

## How it knows the state

An **observer** probes every node on a short interval: every 2 s while any run is
active, every 5 s at rest. It records four facts per node, each yes, no or unknown.

- Reachable
- k3s active
- Ready
- Cordoned

A change in a fact is an event in the event log.

A workflow wait resolves only on such a fact. The observation must be newer than the
moment the run entered the wait, so a stale reading cannot move a run.
An unknown is never treated as a no.

The node table shows a state derived from those facts, never a stored label.

## Operations a node supports

Set per node in `config.yaml` under `operations:`. The Raspberry Pi agents leave out
`poweroff`, because nothing can power them back on remotely.
Rules for the cluster shutdown policy and every timeout: `config.example.yaml`.

## Secrets

Three secrets, never related, each opening one door.

| Variable | Opens |
|---|---|
| `CONSOLE_UI_TOKEN` | the UI and every node action |
| `CONSOLE_NODE_TOKEN` | calls to every node-agent, and the heartbeat each one pushes back |
| `CONSOLE_AUTOMATION_TOKEN` | starting any workflow, for another system such as a home automation. It reads nothing and cannot log in |

An unset token closes its door. It never opens it.

## Node-agent

`cmd/node-agent` is a small daemon on every node. It runs as an unprivileged account.
It exposes fixed endpoints and never a generic "run this command". Kubernetes actions
run as `k3s kubectl` on a master, with that master's own kubeconfig, so console holds
no Kubernetes credential.

A body that does not parse is a 400. It is never a silent success.

## Local development (no real nodes touched)

```sh
go run ./cmd/console -config config.local.yaml
```

Pure mock mode — no process, no network, no node, fastest iteration loop
for the orchestrator/state-machine/UI logic. Visit `http://localhost:8080`
— the one-time UI token is printed to stdout on first run if
`CONSOLE_UI_TOKEN` isn't set.

To exercise a real node-agent process (safe endpoints only — never run
`/reboot` against a node-agent on your own dev machine, default polkit
policy can let the active console user reboot without a prompt):

```sh
NODE_AGENT_TOKEN=<token> go run ./cmd/node-agent \
  -addr :9199 -cert <cert.pem> -key <key.pem> \
  -kubectl-bin kubectl -kubectl-subcommand "" -kubectl-context rancher-desktop
```

then `curl` its endpoints directly, or point a `console` config at it
with `nodeAgent.mock: false`.

For a disposable target where `/reboot` is safe to actually call, use the cluster test
(`cli/tests/e2e`): it installs the real node-agent, with `barekube install node-agent`,
on LXD containers, and runs the console against them.

## Storage and memory

The console keeps its state in SQLite at `storage.path` (default `/config/console.db`).
`/config` is the `console-data` volume of the compose file.
`CONSOLE_DB` overrides the path.
Finished runs, events and timeouts older than `storage.retention` (24 h) are deleted hourly.

The image sets `GOMEMLIMIT=96MiB`, a soft target for the garbage collector, and the
compose file caps the container at 128 MiB. The console logs its memory every minute
(`docker compose logs console | grep "mem go"`). A healthy console sits near 200
goroutines and 20 MiB. See `docs/workflows.md`, section "Memory report".

Build the image yourself:

```sh
docker build -t barekube-console console
```

## Layout

```
cmd/console/main.go     wiring only: config, node-agent clients, service, web server
cmd/node-agent/main.go  flags and TLS listener for the node-side daemon
deploy/                 docker-compose.yml, token generator, check-running.sh
internal/config/        node registry, per-node operations, timing, cluster shutdown policy
internal/events/        the typed event log, a ring buffer with filters
internal/observe/       the observer, the four facts per node and the derived state
internal/flows/         the five workflows, declared as specs on luno/workflow
internal/app/           ops over the node-agents, the prober, and the service the web layer uses
internal/agentd/        the node-agent handlers, with an injectable command runner
internal/nodeagent/     client (HTTPS, pinned certificate, bearer token), master pool, mock
internal/web/           UI, JSON API, the three auth doors, embedded mermaid
```

## Tests

```sh
GOTOOLCHAIN=auto go test -race -cover ./internal/...
```

The bar is 85 % statement coverage per package. Workflow tests run every branch
against a simulated cluster and a fake clock, so no test waits in real time.
`cmd/` holds no logic and is not counted.
