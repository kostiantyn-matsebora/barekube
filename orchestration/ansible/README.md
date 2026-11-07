# Build a cluster with Ansible

Two ways, same playbooks.

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

- Pin a version: `requirements.yml` with `name: kostiantyn_matsebora.barekube`, `version: "0.4.0"`
- Without Galaxy: `ansible-galaxy collection install kostiantyn_matsebora-barekube-<version>.tar.gz` (attached to the release)
- Playbooks: `site`, `remove_node`, `uninstall`

## Inventory

```yaml
all:
  children:
    masters:                  # the first one is the initial master
      hosts:
        master-1: { ansible_host: 192.168.1.10 }
        master-2: { ansible_host: 192.168.1.15 }
        master-3: { ansible_host: 192.168.1.5 }
    agents:
      hosts:
        agent-1: { ansible_host: 192.168.1.50 }
  vars:
    ansible_user: ubuntu
    ansible_become: true
```

Full template: [inventory.example.yml](inventory.example.yml). Keep an odd number of masters.

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

```shell
./play.sh                                                  # add a node: put it in the inventory first
./play.sh remove_node.yml -e barekube_node=agent-2         # drain, delete, uninstall
./play.sh uninstall.yml -e barekube_confirm_uninstall=yes  # destroys the cluster
```

- `remove_node` refuses the first master: put another master first, run `./play.sh`, then remove it
- Machine gone for good: add `-e barekube_node_unreachable=true`
