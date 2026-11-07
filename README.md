# Barekube - bare-metal kubernetes platform

[![CLI tests](https://github.com/kostiantyn-matsebora/barekube/actions/workflows/cli-tests.yml/badge.svg?branch=master)](https://github.com/kostiantyn-matsebora/barekube/actions/workflows/cli-tests.yml)
[![E2E cluster](https://github.com/kostiantyn-matsebora/barekube/actions/workflows/e2e.yml/badge.svg?branch=master)](https://github.com/kostiantyn-matsebora/barekube/actions/workflows/e2e.yml)
[![Release](https://img.shields.io/github/v/release/kostiantyn-matsebora/barekube)](https://github.com/kostiantyn-matsebora/barekube/releases/latest)

Barekube is the low-level foundation of a bare-metal kubernetes platform tailored for home setups, IoT and Edge. A single CLI turns plain Linux nodes into a secure k3s cluster, ready for you to build your own platform on top.

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/barekube-platform-dark.svg">
    <img src="assets/barekube-platform.svg" alt="Barekube: k3s, WireGuard, gVisor and Longhorn prerequisites on bare-metal Linux nodes, with your platform built on top" width="900">
  </picture>
</p>

What barekube brings:

- <img src="assets/icons/k3s.svg" width="20" height="20" align="top"> [k3s](https://k3s.io/) kubernetes cluster: initial master, additional masters (HA) and agent nodes.
- <img src="assets/icons/wireguard.svg" width="20" height="20" align="top"> [WireGuard](https://www.wireguard.com/) network between the nodes.
- <img src="assets/icons/gvisor.svg" width="20" height="20" align="top"> [gVisor](https://gvisor.dev/) - security layer for running containers efficiently and securely.
- <img src="assets/icons/longhorn.svg" width="20" height="20" align="top"> [Longhorn](https://longhorn.io/) storage prerequisites on the nodes.
- <img src="assets/icons/ansible.svg" width="20" height="20" align="top"> [Ansible](https://www.ansible.com/) playbooks that build, change and remove a whole cluster from an inventory.

Everything above that (HA API endpoint, load balancing, ingress, ...) is up to your platform.

## Quick start: build a cluster

```shell
curl -fsSL https://github.com/kostiantyn-matsebora/barekube/releases/latest/download/barekube-ansible.tar.gz | tar xz
cd barekube-ansible
cp inventory.example.yml inventory.yml     # your machines
./play.sh
```

- **Needs:** [Ansible](https://docs.ansible.com/ansible/latest/installation_guide/) or [Docker](https://docs.docker.com/get-docker/) on your computer, and [SSH](https://www.openssh.com/) access to the machines
- **Result:** a cluster, added to `~/.kube/config` as the current context: `kubectl get nodes` works right away
- **Again:** run `./play.sh` to add machines or change settings
- **More:** [settings, removing nodes, teardown](orchestration/ansible/README.md)
- **One node by hand:** [use the CLI](#installing-the-cli)

## Installing the CLI

To install barekube CLI you simply must do the following steps:

- Download the latest release into the destination directory, for instance `$HOME/bin`:

  ```Bash
  mkdir -p $HOME/bin
  wget https://github.com/kostiantyn-matsebora/barekube/releases/latest/download/barekube -O $HOME/bin/barekube
  chmod +x $HOME/bin/barekube
  ```

- Add an alias to your `.bashrc` or `.bash_profile` file:

  ```Bash
  echo "alias barekube='$HOME/bin/barekube'" >> $HOME/.bashrc
  ```

- Reload your shell:

  ```Bash
  source $HOME/.bashrc
  ```

### Verifying the download

Releases are signed. To check yours before running it, use the [GitHub CLI](https://cli.github.com):

```Bash
gh attestation verify $HOME/bin/barekube --repo kostiantyn-matsebora/barekube
```

<details>
<summary>Without the GitHub CLI, using cosign</summary>

Fetch the checksum and signature next to the script and verify both with [cosign](https://docs.sigstore.dev/cosign/system_config/installation/):

```Bash
cd $HOME/bin
BASE=https://github.com/kostiantyn-matsebora/barekube/releases/latest/download
wget -q $BASE/SHA256SUMS $BASE/barekube.sigstore.json
sha256sum --check SHA256SUMS
cosign verify-blob --bundle barekube.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/kostiantyn-matsebora/barekube/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  barekube
rm SHA256SUMS barekube.sigstore.json
```

</details>

`barekube upgrade` checks new releases itself.

## Using the CLI

After installation, you can use barekube CLI by running `barekube --help` command in your terminal. It will show you a help message with available commands:

```shell
barekube - CLI that provides functionality for installing and configuring bare kubernetes based on k3s and gVisor.

Usage:
  barekube COMMAND
  barekube [COMMAND] --help | -h
  barekube --version | -v

Commands:
  install                         Install and configure barekube and dependencies
  install dependencies            Install and configure barekube dependencies
  install initial-master          Install and configure k3s initial (first) master node
  install master-node             Install and configure another k3s master node
  install agent-node              Install and configure k3s agent node
  install gvisor                  Install and configure gVisor for existing k3s node
  install longhorn-dependencies   Install and configure Longhorn storage dependencies
  k3s
  k3s start                       Start k3s master node
  k3s stop                        Stop k3s master node
  k3s uninstall                   Uninstall k3s and all related containers
  k3s killall                     Kill k3s and all k3s related containers
  k3s start-agent                 Start k3s agent node
  k3s stop-agent                  Stop k3s agent node
  upgrade                         Upgrade barekube CLI to the latest verified release

Options:
  --help, -h
    Show this help

  --version, -v
    Show version number


MIT License: https://github.com/kostiantyn-matsebora/barekube/blob/master/LICENSE
Copyright (c) 2025 Kostiantyn Matsebora
```

## Running barekube from scripts

By default the install commands ask you questions. To run them from a script, pass the answers as flags and add `--yes`. Barekube then stops with an error if something is missing, instead of waiting for you to type it.

The join token is not a flag. Put it in the `K3S_TOKEN` environment variable, so it stays out of `ps` output and your shell history.

```shell
# First master
barekube install initial-master --yes --tls-san k8s.home.lan --disable traefik

# Another master or an agent
export K3S_TOKEN='<token printed by the first master>'
barekube install master-node --yes --server https://192.168.1.10:6443
barekube install agent-node  --yes --server https://192.168.1.10:6443
```

When an install command finishes it prints `installed` or `already installed`, so a script can tell whether anything changed.

## gVisor as the default

barekube installs two `RuntimeClass` objects on the control plane: `gvisor` (the sandbox) and `runc` (the normal runtime). A pod picks one with `runtimeClassName`. A pod that names none runs with the normal runtime.

To put every new pod in the gVisor sandbox unless it says otherwise, add `--gvisor-default` when you install a control-plane node, or switch it later on any control-plane node:

```shell
barekube install initial-master --gvisor-default
barekube install gvisor --default       # a cluster that is already running
barekube install gvisor --no-default    # and back
```

This installs a Kubernetes admission policy that sets `runtimeClassName: gvisor` on new pods that name no class. It needs a Kubernetes version that has `MutatingAdmissionPolicy`. It leaves alone:

- the `kube-system`, `kube-public`, `kube-node-lease` and `longhorn-system` namespaces;
- pods that use the host network, PID or IPC namespaces, and pods with a privileged container.

gVisor can't run those. For any other pod that needs the host, set `runtimeClassName: runc`.
