# Inventory specification

The complete description of the inventory the playbooks read. A starting point
is [inventory.example.yml](inventory.example.yml); the short version is in the
[README](README.md#inventory).

Defaults live in [roles/barekube/defaults/main.yml](roles/barekube/defaults/main.yml).
Every `barekube_*` setting is optional.

## Full example

Every setting, with its default (except where noted).

```yaml
all:
  children:
    masters:                      # required, at least one host; the first is the initial master
      hosts:
        192.168.1.10:
        192.168.1.15:
        192.168.1.5:
          barekube_address: 192.168.1.5   # per host, optional
    agents:                       # optional
      hosts:
        192.168.1.50:
  vars:
    ansible_user: ubuntu
    ansible_become: true

    barekube_version: v0.4.1
    barekube_longhorn: true
    barekube_gvisor_default: false
    barekube_disable: []                 # for example [servicelb, traefik]
    barekube_tls_san: []                 # for example [k8s.home.lan]
    barekube_join_endpoint: https://192.168.1.10:6443   # default: the first master
    barekube_extra_args: ""
    barekube_cluster_name: default
    barekube_kubeconfig: ~/.kube/config  # default: $HOME/.kube/config

    barekube_verify_signature: true
    barekube_cli_path: ""
    barekube_release_url: https://github.com/kostiantyn-matsebora/barekube/releases/download/v0.4.1
    barekube_signer_identity: ^https://github\.com/kostiantyn-matsebora/barekube/\.github/workflows/release\.yml@refs/tags/v

    barekube_ready_timeout: 300
    barekube_drain_timeout: 300
    barekube_install_attempts: 4
    barekube_install_retry_delay: 10

    barekube_show_output: false
```

## Changing a running cluster

The inventory is the desired state. Edit it and run the playbook again:

| Change | What happens |
|---|---|
| host added to `masters` or `agents` | the node joins |
| `barekube_disable`, `barekube_tls_san`, `barekube_extra_args` | each server is reconfigured and restarted, one at a time; agents are untouched |
| `barekube_gvisor_default` | applied from a master, no restart |
| host removed from the inventory | nothing: run `remove_node` first, then delete the host |

Running the playbook with nothing changed changes nothing.

## Groups

| Group | Required | |
|---|---|---|
| `masters` | yes | k3s servers. The first host is the initial master; the others join it. Keep an odd number. |
| `agents` | no | k3s agents (workers). |

Hosts are IP addresses or names. For a name that does not resolve, set
`ansible_host: <IP>` on the host. Host names are what `-e barekube_node=` takes.

## Connection

Plain Ansible variables, set on `all`, a group or a host.

| Variable | |
|---|---|
| `ansible_user` | SSH user |
| `ansible_become` | `true` to use sudo; the install needs root |
| `ansible_host` | address to connect to, when the inventory name does not resolve |
| `ansible_port` | SSH port |
| `ansible_ssh_private_key_file` | key to log in with |

## The cluster

| Variable | Type | Default | |
|---|---|---|---|
| `barekube_version` | string | this release | barekube release (a tag, never a branch). Checksum always checked |
| `barekube_longhorn` | bool | `true` | install the packages Longhorn needs on every node |
| `barekube_gvisor_default` | bool | `false` | run new pods in the gVisor sandbox unless they name another RuntimeClass |
| `barekube_disable` | list | `[]` | k3s components to turn off: `servicelb`, `traefik`, `local-storage`, `metrics-server`, ... Becomes `--disable` flags |
| `barekube_tls_san` | list | `[]` | extra names for the API server certificate. The first master's address and the join endpoint's host are always included. Becomes `--tls-san` flags |
| `barekube_join_endpoint` | URL | `https://<first master>:6443` | where nodes join. Set it to your virtual IP or load balancer; barekube installs none |
| `barekube_extra_args` | string | `""` | anything else to pass to the k3s server |
| `barekube_address` | string | `ansible_host`, else the name | per host: the address the other nodes reach it on |
| `barekube_cluster_name` | string | `default` | names the kubeconfig context `barekube-<name>` |
| `barekube_kubeconfig` | path | `~/.kube/config` | the kubeconfig the cluster is added to; the cluster becomes its current context |

`barekube_disable` and `barekube_tls_san` must be lists. On the command line:
`-e '{"barekube_disable": ["traefik"]}'`.

## The barekube CLI

| Variable | Type | Default | |
|---|---|---|---|
| `barekube_verify_signature` | bool | `true` | check the Sigstore signature of the release (needs `cosign` on this machine) |
| `barekube_cli_path` | path | `""` | install this local file instead of downloading a release (development) |
| `barekube_release_url` | URL | the GitHub release of `barekube_version` | where the release files are downloaded from |
| `barekube_signer_identity` | regex | the release workflow of this repository | the identity the signature must come from |

## Timing and retries

| Variable | Type | Default | |
|---|---|---|---|
| `barekube_ready_timeout` | seconds | `300` | how long to wait for a node to become Ready |
| `barekube_drain_timeout` | seconds | `300` | how long to wait for pods to leave a node being removed |
| `barekube_install_attempts` | number | `4` | tries per install command (the k3s download can fail) |
| `barekube_install_retry_delay` | seconds | `10` | wait after the first failure; doubles after each next one (10, 20, 40) |

## Output

| Variable | Type | Default | |
|---|---|---|---|
| `barekube_show_output` | bool | `false` | show the output of the install and join commands. Hidden by default because the join token is in their environment |

## Playbook inputs

Not for the inventory: pass them with `-e`.

| Variable | Playbook | |
|---|---|---|
| `barekube_node` | `remove_node` | the node to remove, as named in the inventory |
| `barekube_node_unreachable` | `remove_node` | `true` to skip draining and uninstalling a machine that is gone |
| `barekube_confirm_uninstall` | `uninstall` | must be `yes`; destroys the cluster |
