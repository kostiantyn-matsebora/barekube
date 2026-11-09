# console

The optional web UI and control plane of a barekube cluster. It runs from a Docker
image on any machine that outlives the cluster, and manages the nodes through a small
**node-agent** that barekube installs on each of them.
Its own web UI is the only way in, apart from one narrow automation route.

```
 browser ──► console (Docker) ──HTTPS, pinned certificate, token──► node-agent on every node
                  ▲                                                       │
                  └────────────────── heartbeat, same token ──────────────┘
```

Nothing here is needed to run a cluster. Barekube builds and runs the cluster without it.

Every operation is a **workflow** that moves only on what it observes. Nothing is
assumed. A reboot is not "joining" because a timer ran out. It is "joining" because
the node was seen unreachable and then reachable and Ready again.

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

## Install

Three steps: the console, the node-agents, and the pinned certificates that connect them.

### 1. The console, with Docker Compose

```sh
cd console/deploy          # from a checkout, or just the three files of that directory
./generate-tokens.sh       # .env with CONSOLE_UI_TOKEN and CONSOLE_NODE_TOKEN
docker compose up -d
```

The image is `ghcr.io/kostiantyn-matsebora/barekube-console`, published with every
release for amd64, arm64 and arm/v7, signed with Sigstore. `CONSOLE_VERSION` in `.env`
pins a release (default `latest`). The compose file needs `config/config.yaml` and
`config/credentials/`, which step 3 makes.

The UI token opens the UI. The node token is the secret the node-agents share
with the console. They are made unrelated on purpose.

### 2. The node-agents

With Ansible, from the same inventory that builds the cluster, which is the way
for more than a node or two:

```yaml
all:
  vars:
    barekube_console_agent: true                      # off by default; a host can override it
    barekube_console_address: http://192.168.1.5:8080 # where the console is
    # barekube_console_node_token: ...   # optional: made and printed on the first run
    barekube_console_export_dir: ./console/deploy/config            # makes step 3 for you
```

The playbook prints the node token it made; give it to the console as `CONSOLE_NODE_TOKEN`
(`generate-tokens.sh` is optional: any 16+ character values in `.env` work, and the playbook
uses the one you set in `barekube_console_node_token`). On later runs it reads the token back
from the agents, so it stays the same.

See the [inventory reference](../orchestration/ansible/INVENTORY.md#console-agents-optional)
for every setting. A node's role comes from its group, and its port and operations
default from the role.

With the CLI, on a node:

```sh
CONSOLE_NODE_TOKEN=... barekube install node-agent --console-addr http://192.168.1.5:8080 --yes
```

Options per node: `--node-name`, `--port`, `--operations`, `--etcd-metrics-url`,
`--api-vip` with `--api-gateway`, `--heartbeat-interval`; `--remove` takes the
node-agent off again. It prints `fingerprint <hex>`, which step 3 needs. The CLI
downloads the node-agent of its own version, checks the checksum and the Sigstore
signature, and changes only what differs when run again.

### 3. The configuration

Copy `config.example.yaml` to `config/config.yaml` and list the nodes, one entry per
node (the name, port and operations of the node-agent). Put each node's fingerprint,
which is not a secret, in `config/credentials/<node name>`. The Ansible export writes
both files from the inventory.

The console trusts a node-agent only if its certificate has that fingerprint, so a
different machine on the node's address is refused.

### What a node-agent is allowed

The agent runs as the system account `barekube-agent`, which cannot log in and has no
sudo. A polkit rule lets it reboot, start/stop/kill the one k3s unit of its node,
and, on masters, power off. `--operations` narrows that list: an operation left out
is not granted. The one privileged command that is no polkit action, the local etcd
snapshot of a cluster shutdown, has a sudoers line for that exact command. The token
is in a file only root reads.

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
