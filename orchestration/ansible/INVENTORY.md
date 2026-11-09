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

    barekube_version: v0.5.0
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
    barekube_release_url: https://github.com/kostiantyn-matsebora/barekube/releases/download/v0.5.0
    barekube_signer_identity: ^https://github\.com/kostiantyn-matsebora/barekube/\.github/workflows/release\.yml@refs/tags/v

    barekube_ready_timeout: 300
    barekube_drain_timeout: 300
    barekube_install_attempts: 4
    barekube_install_retry_delay: 10

    barekube_show_output: false

    # The console agents are off by default. Enable them with these three:
    barekube_console_agent: false
    barekube_console_address: http://192.168.1.5:8080
    # barekube_console_node_token: "{{ vault_console_node_token }}"   # optional: made and printed when not set
```

Hosts are written as objects only to override something, for example
`192.168.1.5: { barekube_console_port: 9199 }`.

## Changing a running cluster

The inventory is the desired state. Edit it and run the playbook again:

| Change | What happens |
|---|---|
| host added to `masters` or `agents` | the node joins |
| `barekube_disable`, `barekube_tls_san`, `barekube_extra_args` | each server is reconfigured and restarted, one at a time; agents are untouched |
| `barekube_gvisor_default` | applied from a master, no restart |
| host removed from the inventory | nothing: run `remove_node` first, then delete the host |

Running the playbook with nothing changed changes nothing.

Turning a component **off** removes it from the cluster. A component that was
never deployed comes up when you stop disabling it. Turning back **on** one that
ran before is not reliable in k3s: it keeps its record of the component (an
`Addon`) and does not always recreate the Helm chart. If it does not come back,
delete the record and restart k3s on a master:

```shell
kubectl -n kube-system delete addon traefik   # or servicelb, ...
sudo systemctl restart k3s
```

Test the change on a spare cluster first.

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

## The console agents

Optional and off by default. The [console](../../console/README.md) is a web UI
that manages the nodes through a small agent on each one. To use it, set three
variables; every other one has a default.

```yaml
all:
  vars:
    barekube_console_agent: true
    barekube_console_address: http://192.168.1.5:8080
    # barekube_console_node_token: "{{ vault_console_node_token }}"   # optional: made and printed when not set
```

The token is the `CONSOLE_NODE_TOKEN` of the console. Keep it in Ansible Vault or
pass it with `-e`. It is checked for length and characters before anything is
installed, hidden from the output and written only to a file that root can read
on each node.

Nothing is required per node. The role of a node comes from its group, never
from a variable, and every setting below has a default for that role. Set one
only to override it, on `all`, on `masters` or `agents` (shared by the group) or
on one host. The most specific one wins, as for any Ansible variable.

```yaml
all:
  children:
    masters:
      hosts:
        192.168.1.10:
        192.168.1.5:
          barekube_console_port: 9199                 # one host
          barekube_console_operations: [reboot, standby]
      vars:
        barekube_console_api_vip: 192.168.1.100       # all masters
        barekube_console_api_gateway: 192.168.1.1
    agents:
      hosts:
        192.168.1.50:
  vars:
    barekube_console_agent: true
    barekube_console_address: http://192.168.1.5:8080
    # barekube_console_node_token: "{{ vault_console_node_token }}"   # optional: made and printed when not set
```

`barekube_console_agent` is decided per host, with normal Ansible precedence.
Enable it for everything and leave one node out, or enable it for one group or
one host only. A node without it gets no agent and is left out of the exported
console configuration.

```yaml
all:
  children:
    masters:
      hosts:
        192.168.1.10:
        192.168.1.15:
    agents:
      hosts:
        192.168.1.50:
        192.168.1.30:
          barekube_console_agent: false     # this one is not managed by the console
  vars:
    barekube_console_agent: true            # all the others are
    barekube_console_address: http://192.168.1.5:8080
    # barekube_console_node_token: "{{ vault_console_node_token }}"   # optional: made and printed when not set
```

The address and the token are checked only on the hosts that have the agent.
Ansible prints a warning that the host group `barekube_console_agent_True`
matched nothing when no host has it; that is expected.

| Variable | Type | Default | |
|---|---|---|---|
| `barekube_console_agent` | bool | `false` | install the node-agent on this node; set it on `all`, a group or a host |
| `barekube_console_address` | URL | required | where the console runs; the nodes send their heartbeat here |
| `barekube_console_node_token` | string | made for you | the secret shared with the console (16 or more characters of `A-Z a-z 0-9 . _ ~ + / = -`). Leave it out and the playbook makes one on the first install, prints it at the end for the console's `CONSOLE_NODE_TOKEN`, and reads it back from the agents on later runs. Set it to choose your own or to change it |
| `barekube_console_node_name` | string | the inventory name | the name of the node in the console configuration |
| `barekube_console_port` | number | `9091` | the port of the agent |
| `barekube_console_operations` | list | by role | what the console may do: any of `reboot`, `standby`, `rejoin`, `poweroff`. Empty means the role default below |
| `barekube_console_operations_master` | list | `[reboot, standby, rejoin, poweroff]` | the default for masters |
| `barekube_console_operations_agent` | list | `[reboot, standby, rejoin]` | the default for agents. An agent cannot be allowed to power off |
| `barekube_console_etcd_metrics_url` | URL | the agent's own | masters: the local etcd metrics endpoint |
| `barekube_console_api_vip`, `barekube_console_api_gateway` | address | empty | masters: the API virtual IP and the gateway used to find its interface; both enable the cold-start helper |
| `barekube_console_heartbeat_interval` | duration | `5s` | how often a node reports |
| `barekube_console_export_dir` | path | `""` | write `config.yaml` and `credentials/<node>` for the console here, from this inventory |
| `barekube_node_agent_dir` | path | `""` | install the node-agent from this directory (`node-agent-linux-<arch>`) instead of the release (development) |

The permissions of a node follow its operations: a node without `poweroff` gets
no power-off permission, so a bug in the console or the agent still cannot turn
it off.

The agents are installed after the cluster is up. The exported configuration has
the same values the agents got, so the console and the nodes agree. Take the
agents off with `remove_console_agent.yml`; the cluster is not touched.

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

`remove_console_agent` takes no input; limit it to some nodes with `--limit`.
