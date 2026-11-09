# barekube console

<img src="../assets/icons/console.svg" width="20" height="20" align="top"> The optional control plane of a barekube cluster, with a web UI. Nothing needs it to run a cluster.

- See every node: reachable, k3s running, Ready, cordoned.
- Reboot, stand by, power off and rejoin nodes: cordon, drain, act, then wait until the node is seen back.
- Shut the whole cluster down in the right order, and start it again after the nodes are powered on.
- Every operation is a workflow that moves only on what it observes, never on a timer.

```
browser ──► console (Docker) ──HTTPS, pinned certificate, token──► node-agent on every node
```

## Install

Two parts: a **node-agent** on every node, and the **console** on one machine that stays up when the cluster is off.
They share one secret, the **node token**.

### 1. Install the node-agents

With Ansible, add to the inventory you build the cluster with:

```yaml
all:
  vars:
    barekube_console_agent: true                       # off by default; a host can override it
    barekube_console_address: http://192.168.1.5:8080  # where the console will run
    barekube_console_export_dir: console/deploy/config # writes the console configuration (step 2)
```

Run the playbook. It installs the agents and **prints the node token** at the end. Note it.

Without Ansible, on each node, with a token you made (`openssl rand -hex 24`):

```sh
CONSOLE_NODE_TOKEN=<token> barekube install node-agent --yes --console-addr http://192.168.1.5:8080
```

Ports, node names and what the console may do with a node all have defaults and can be overridden:
[all settings](docs/agents.md).

### 2. Start the console

On the console machine, with Docker Compose:

```sh
cd console/deploy
CONSOLE_NODE_TOKEN=<the node token> ./generate-tokens.sh   # writes .env, with a random UI token
docker compose up -d
```

The console needs `config/` next to the compose file: the nodes, and each agent's certificate fingerprint.
With `barekube_console_export_dir` above, Ansible has written it. By hand: [how](docs/agents.md#2-the-configuration).

### 3. Sign in

Open `http://<console machine>:8080` and sign in with `CONSOLE_UI_TOKEN` from `.env`.
Every node should show as reachable within seconds.

The UI has no TLS of its own: keep it on a trusted network or behind your own proxy.

## More

- [Every Ansible variable and CLI option, the configuration, upgrading](docs/agents.md)
- [What a node-agent is allowed to do](docs/agents.md#what-a-node-agent-is-allowed)
- [How it works: workflows, state, secrets](docs/reference.md)
- [Workflow design](docs/workflows.md)
- [Development](DEVELOPMENT.md)
