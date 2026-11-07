# Building a cluster with Ansible

The playbooks come two ways, with the same content. Pick one.

## A. Release archive with `play.sh`

Needs [Ansible](https://docs.ansible.com/ansible/latest/installation_guide/) or [Docker](https://docs.docker.com/get-docker/).

```shell
curl -fsSL https://github.com/kostiantyn-matsebora/barekube/releases/latest/download/barekube-ansible.tar.gz | tar xz
cd barekube-ansible
cp inventory.example.yml inventory.yml     # your machines
./play.sh
```

Every release publishes the archive with a checksum, a signature and a provenance attestation. To verify it first: download it and run `gh attestation verify barekube-ansible.tar.gz --repo kostiantyn-matsebora/barekube`.

## B. Ansible collection

Needs [Ansible](https://docs.ansible.com/ansible/latest/installation_guide/) 2.18 or newer.

```shell
ansible-galaxy collection install kostiantyn_matsebora.barekube
ansible-playbook -i inventory.yml kostiantyn_matsebora.barekube.site
```

Pin a version in a `requirements.yml`:

```yaml
collections:
  - name: kostiantyn_matsebora.barekube
    version: "0.4.0"
```

Without Galaxy, install the collection file attached to the release: `ansible-galaxy collection install kostiantyn_matsebora-barekube-<version>.tar.gz`.

The playbooks are `site` (build the cluster), `remove_node` and `uninstall`, for example `ansible-playbook -i inventory.yml kostiantyn_matsebora.barekube.remove_node -e barekube_node=agent-2`.

The rest of this page uses `./play.sh`; with the collection, run the same playbooks with `ansible-playbook` as above.

`play.sh` uses the Ansible on your machine, or, if there is none, builds a small Docker image with everything the playbooks need (Ansible, an SSH client and `cosign`). Your SSH agent or `~/.ssh` keys are used to reach the machines. To install Ansible yourself: `pipx install 'ansible-core>=2.18,<2.22'`.

## Logging in to the machines

| Machines accept | Do |
|---|---|
| your SSH key | nothing: your SSH agent or `~/.ssh` keys are used |
| a username and password | `./play.sh -k`, and enter the password when asked. Add `-K` if `sudo` asks for a password too. Set `ansible_user` in the inventory. |

Without Docker, password login needs [`sshpass`](https://sshpass.sourceforge.net/) on your computer; the Docker image has it. Do not write passwords in the inventory; if you must, encrypt them with [Ansible Vault](https://docs.ansible.com/ansible/latest/vault_guide/).

The inventory can be anywhere: `./play.sh -i ~/clusters/home/inventory.yml`. Without `-i` it reads `inventory.yml` in the directory you run it from. Without a playbook it runs `site.yml`. Anything else is passed to `ansible-playbook`.

## The inventory

The hosts under `masters` and `agents` are your cluster. A minimal inventory ([the full template](inventory.example.yml)):

```yaml
all:
  children:
    masters:
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

 The first master is the initial one: the others join it. Keep an odd number of masters. Settings are variables, all optional:

| Variable | Default | What it does |
|---|---|---|
| `barekube_version` | the version this playbook was released with | The barekube release to install. Its checksum and Sigstore signature are checked first (this needs `cosign`; set `barekube_verify_signature: false` to check only the checksum). |
| `barekube_longhorn` | `true` | Install the packages Longhorn needs on every node. |
| `barekube_gvisor_default` | `false` | Run new pods in the gVisor sandbox unless they say otherwise. See [gVisor as the default](../../README.md#gvisor-as-the-default). |
| `barekube_disable` | `[]` | k3s components to turn off, for example `[servicelb, traefik]`. |
| `barekube_tls_san` | `[]` | Extra names for the API server certificate. |
| `barekube_join_endpoint` | the first master | Where nodes join. Set it to your own virtual IP or load balancer if you have one; barekube does not install one. |
| `barekube_extra_args` | empty | Anything else to pass to the k3s server. |
| `barekube_kubeconfig` | `~/.kube/config` | The kubeconfig the cluster is added to, as the current context `barekube-default`. Your other clusters in it are kept. |
| `barekube_cluster_name` | `default` | Names the context, `barekube-<name>`. Set it if you build more than one cluster. |

The join token is read from the first master into memory and handed to the other nodes through the environment. It is never written to a file or shown in the output.

## Changing a running cluster

```shell
# Add a node: put it in the inventory and run the playbook again
./play.sh

# Take a node out: drain it, delete it from Kubernetes (a master also leaves etcd) and uninstall k3s from it
./play.sh remove_node.yml -e barekube_node=agent-2

# Remove k3s from every node and the cluster from your kubeconfig. This destroys the cluster and its data.
./play.sh uninstall.yml -e barekube_confirm_uninstall=yes
```

The first master is the one the others join, so `remove_node.yml` refuses it: put another master first in the inventory and run `./play.sh`, then remove the old one. For a machine that is gone for good, add `-e barekube_node_unreachable=true`: the node is then only deleted from the cluster.
