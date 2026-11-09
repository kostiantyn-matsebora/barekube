# Reference

How the console works. For installing it see the [README](../README.md).

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

Design and the diagrams: [workflows.md](workflows.md).


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
Rules for the cluster shutdown policy and every timeout: [`config.example.yaml`](../config.example.yaml).


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


## Storage and memory

The console keeps its state in SQLite at `storage.path` (default `/config/console.db`).
`/config` is the `console-data` volume of the compose file.
`CONSOLE_DB` overrides the path.
Finished runs, events and timeouts older than `storage.retention` (24 h) are deleted hourly.

The image sets `GOMEMLIMIT=96MiB`, a soft target for the garbage collector, and the
compose file caps the container at 128 MiB. The console logs its memory every minute
(`docker compose logs console | grep "mem go"`). A healthy console sits near 200
goroutines and 20 MiB. See [workflows.md](workflows.md), section "Memory report".

Build the image yourself:

```sh
docker build -t barekube-console console
```

