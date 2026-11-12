<img src="https://raw.githubusercontent.com/kostiantyn-matsebora/barekube/master/assets/barekube-logo.png" alt="barekube" width="96" align="right">

# barekube

[![CLI tests](https://github.com/kostiantyn-matsebora/barekube/actions/workflows/cli-tests.yml/badge.svg?branch=master)](https://github.com/kostiantyn-matsebora/barekube/actions/workflows/cli-tests.yml)
[![E2E cluster](https://github.com/kostiantyn-matsebora/barekube/actions/workflows/e2e.yml/badge.svg?branch=master)](https://github.com/kostiantyn-matsebora/barekube/actions/workflows/e2e.yml)
[![Release](https://img.shields.io/github/v/release/kostiantyn-matsebora/barekube)](https://github.com/kostiantyn-matsebora/barekube/releases/latest)
[![Galaxy](https://img.shields.io/badge/dynamic/json?url=https%3A%2F%2Fgalaxy.ansible.com%2Fapi%2Fv3%2Fplugin%2Fansible%2Fcontent%2Fpublished%2Fcollections%2Findex%2Fkostiantyn_matsebora%2Fbarekube%2F&query=%24.highest_version.version&label=Galaxy&prefix=v&color=blue&logo=ansible)](https://galaxy.ansible.com/ui/repo/published/kostiantyn_matsebora/barekube/)

Build, change and remove a bare-metal Kubernetes cluster from an inventory:

- [k3s](https://k3s.io/) with HA masters and agents
- [WireGuard](https://www.wireguard.com/) network between the nodes
- [gVisor](https://gvisor.dev/) sandbox, optionally the default for pods
- [Longhorn](https://longhorn.io/) storage prerequisites

Part of [barekube](https://github.com/kostiantyn-matsebora/barekube), the low-level foundation of a bare-metal Kubernetes platform for home setups, IoT and Edge. [Source](https://github.com/kostiantyn-matsebora/barekube) | [Issues](https://github.com/kostiantyn-matsebora/barekube/issues) | [Releases](https://github.com/kostiantyn-matsebora/barekube/releases)

Playbooks: `site` (build), `remove_node`, `uninstall`, `remove_console_agent`. Two ways to get them:

## A. Release archive

Needs [Ansible](https://docs.ansible.com/ansible/latest/installation_guide/) or [Docker](https://docs.docker.com/get-docker/).

```shell
curl -fsSL https://github.com/kostiantyn-matsebora/barekube/releases/latest/download/barekube-ansible.tar.gz | tar xz
cd barekube-ansible
cp inventory.example.yml inventory.yml     # your machines
./play.sh
```

Verify first: `gh attestation verify barekube-ansible.tar.gz --repo kostiantyn-matsebora/barekube`

## B. Ansible collection

Needs [Ansible](https://docs.ansible.com/ansible/latest/installation_guide/) 2.18+.

```shell
ansible-galaxy collection install kostiantyn_matsebora.barekube
ansible-playbook -i inventory.yml kostiantyn_matsebora.barekube.site
```

- Pin a version: `requirements.yml` with `name: kostiantyn_matsebora.barekube`, `version: "0.5.0"`
- Without Galaxy: `ansible-galaxy collection install kostiantyn_matsebora-barekube-<version>.tar.gz` (attached to the release)
- Playbooks: `site`, `remove_node`, `uninstall`

## Inventory

```yaml
all:
  children:
    masters:                  # the first one is the initial master
      hosts:
        192.168.1.10:
        192.168.1.15:
        192.168.1.5:
    agents:
      hosts:
        192.168.1.50:
  vars:
    ansible_user: ubuntu
    ansible_become: true

    barekube_disable: [servicelb, traefik]   # k3s components to turn off (default: [])
    barekube_tls_san: []                     # extra API server certificate names (default: [])
```

- Hosts are IP addresses or names. For a name that does not resolve, add `ansible_host: <IP>`
- `-e barekube_node=` takes the name as written in the inventory
- Keep an odd number of masters
- Full template: [inventory.example.yml](inventory.example.yml)
- Every option: [INVENTORY.md](INVENTORY.md)

## Settings

All optional, set in the inventory.

| Variable | Default | |
|---|---|---|
| `barekube_version` | this release | barekube release to install, checksum and signature checked (`cosign`; `barekube_verify_signature: false` skips the signature) |
| `barekube_longhorn` | `true` | Longhorn packages on every node |
| `barekube_gvisor_default` | `false` | [gVisor as the default](../../README.md#gvisor-as-the-default) |
| `barekube_disable` | `[]` | k3s components to turn off, e.g. `[servicelb, traefik]` |
| `barekube_tls_san` | `[]` | extra API server certificate names |
| `barekube_join_endpoint` | first master | where nodes join: your VIP or load balancer |
| `barekube_extra_args` | empty | extra k3s server arguments |
| `barekube_kubeconfig` | `~/.kube/config` | where the cluster is added, as context `barekube-default` |
| `barekube_cluster_name` | `default` | context name: `barekube-<name>` |
| `barekube_console_agent` | `false` | install the agents of the optional [console](../../console/README.md), see [Console agents](#console-agents-optional) |
| `barekube_install_attempts` | `4` | tries per install command; the wait doubles after each failure |
| `barekube_install_retry_delay` | `10` | seconds to wait after the first failure |

More settings, with types: [INVENTORY.md](INVENTORY.md).

## Console agents (optional)

Off by default. Installs a small agent on the nodes, so the [console](../../console/README.md) can manage them. Two settings turn it on, everything else has a default:

```yaml
all:
  vars:
    barekube_console_agent: true                        # default false
    barekube_console_address: http://192.168.1.5:8080   # where the console will run
```

Run the playbook. The agents share a token with the console: the playbook makes one on the first run and **prints it** at the end (give it to the console as `CONSOLE_NODE_TOKEN`), then reads it back from the agents on later runs. To choose your own, set `barekube_console_node_token`.

Nothing is needed per node. A node's role comes from its group and its defaults follow the role. To override, set the variable on `all`, on a group (`masters` or `agents`) or on one host, where the most specific wins. `barekube_console_agent` itself works the same way:

```yaml
all:
  children:
    masters:
      hosts:
        192.168.1.10:
        192.168.1.15:
          barekube_console_port: 9199                        # this node only
          barekube_console_operations: [reboot, standby]     # and it may only do these
    agents:
      hosts:
        192.168.1.50:
        192.168.1.30:
          barekube_console_agent: false                      # no agent on this node
  vars:
    barekube_console_agent: true
    barekube_console_address: http://192.168.1.5:8080
```

| Variable | Default | |
|---|---|---|
| `barekube_console_agent` | `false` | install the agent on the node |
| `barekube_console_address` | required | the console's address, where the nodes send heartbeats |
| `barekube_console_node_token` | made for you | the shared secret: 16 or more characters of `A-Z a-z 0-9 . _ ~ + / = -`; best from Ansible Vault |
| `barekube_console_node_name` | the inventory name | the node's name in the console |
| `barekube_console_port` | `9091` | the port of the agent |
| `barekube_console_operations` | by role | what the console may do with the node: `reboot`, `standby`, `rejoin`, `poweroff`. Masters: all four. Agents: without `poweroff` |
| `barekube_console_heartbeat_interval` | `5s` | how often the node reports |
| `barekube_console_etcd_metrics_url` | the agent's own | masters: the local etcd metrics endpoint |
| `barekube_console_api_vip`, `barekube_console_api_gateway` | empty | masters: the API virtual IP and the gateway that finds its interface; both enable the cold-start helper |
| `barekube_console_export_dir` | empty | write the console's `config.yaml` and the nodes' certificate fingerprints here, from this inventory |

Take the agents off with `ansible-playbook -i inventory.yml remove_console_agent.yml` (the cluster is not touched). Types and details: [INVENTORY.md](INVENTORY.md#the-console-agents).

## Logging in

| Machines accept | Run |
|---|---|
| SSH key | `./play.sh` (agent or `~/.ssh` keys) |
| password | `./play.sh -k` (`-K` if sudo asks too) |

Password login without Docker needs [`sshpass`](https://sshpass.sourceforge.net/).

## `play.sh`

```shell
./play.sh                                    # ./inventory.yml, playbook site
./play.sh -i ~/clusters/home/inventory.yml   # inventory anywhere
./play.sh remove_node.yml -e barekube_node=agent-2
```

Uses Ansible if installed, else Docker (`BAREKUBE_USE_DOCKER=1` forces Docker).

## Change a running cluster

The inventory is the desired state: edit it and run `./play.sh` again. The cluster follows, and only what differs changes.

- Add or remove nodes: add the host to `masters` or `agents` and run the playbook. To remove one, run `remove_node` first, then delete the host
- Change `barekube_disable`, `barekube_tls_san` or `barekube_extra_args`: the servers are reconfigured and restarted one at a time
- Change `barekube_gvisor_default`: applied from a master, no restart
- Turning a component that ran before back on is not reliable in k3s and may need manual steps: [INVENTORY.md](INVENTORY.md#changing-a-running-cluster)

```shell
./play.sh                                                  # add a node: put it in the inventory first
./play.sh remove_node.yml -e barekube_node=agent-2         # drain, delete, uninstall
./play.sh uninstall.yml -e barekube_confirm_uninstall=yes  # destroys the cluster
```

- `remove_node` refuses the first master: put another master first, run `./play.sh`, then remove it
- Machine gone for good: add `-e barekube_node_unreachable=true`
