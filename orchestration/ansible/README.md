# Building a cluster with Ansible

```shell
curl -fsSL https://github.com/kostiantyn-matsebora/barekube/releases/latest/download/barekube-ansible.tar.gz | tar xz
cd barekube-ansible
cp inventory.example.yml inventory.yml     # your machines
./play.sh
```

Every release publishes the archive with a checksum, a signature and a provenance attestation. To verify it first: download it and run `gh attestation verify barekube-ansible.tar.gz --repo kostiantyn-matsebora/barekube`.

`play.sh` uses the Ansible on your machine, or, if there is none, builds a small Docker image with everything the playbooks need (Ansible, an SSH client and `cosign`). Your SSH agent or `~/.ssh` keys are used to reach the machines. To install Ansible yourself: `pipx install 'ansible-core>=2.18,<2.22'`.

## Logging in to the machines

| Machines accept | Do |
|---|---|
| your SSH key | nothing: your SSH agent or `~/.ssh` keys are used |
| a username and password | `./play.sh -k`, and enter the password when asked. Add `-K` if `sudo` asks for a password too. Set `ansible_user` in the inventory. |

Without Docker, password login needs [`sshpass`](https://sshpass.sourceforge.net/) on your computer; the Docker image has it. Do not write passwords in the inventory; if you must, encrypt them with [Ansible Vault](https://docs.ansible.com/ansible/latest/vault_guide/).

Without `-i` it reads `inventory.yml`, and without a playbook it runs `site.yml`. Anything else is passed to `ansible-playbook`.

## The inventory

The hosts under `masters` and `agents` are your cluster. The first master is the initial one: the others join it. Keep an odd number of masters. Settings are variables, all optional:

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
./play.sh remove-node.yml -e barekube_node=agent-2

# Remove k3s from every node and the cluster from your kubeconfig. This destroys the cluster and its data.
./play.sh uninstall.yml -e barekube_confirm_uninstall=yes
```

The first master is the one the others join, so `remove-node.yml` refuses it: put another master first in the inventory and run `./play.sh`, then remove the old one. For a machine that is gone for good, add `-e barekube_node_unreachable=true`: the node is then only deleted from the cluster.
