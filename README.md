# Barekube - bare-metal kubernetes platform

[![CLI tests](https://github.com/kostiantyn-matsebora/barekube/actions/workflows/cli-tests.yml/badge.svg?branch=master)](https://github.com/kostiantyn-matsebora/barekube/actions/workflows/cli-tests.yml)
[![E2E cluster](https://github.com/kostiantyn-matsebora/barekube/actions/workflows/e2e.yml/badge.svg?branch=master)](https://github.com/kostiantyn-matsebora/barekube/actions/workflows/e2e.yml)
[![Release](https://img.shields.io/github/v/release/kostiantyn-matsebora/barekube)](https://github.com/kostiantyn-matsebora/barekube/releases/latest)
[![Galaxy](https://img.shields.io/badge/dynamic/json?url=https%3A%2F%2Fgalaxy.ansible.com%2Fapi%2Fv3%2Fplugin%2Fansible%2Fcontent%2Fpublished%2Fcollections%2Findex%2Fkostiantyn_matsebora%2Fbarekube%2F&query=%24.highest_version.version&label=Galaxy&prefix=v&color=blue&logo=ansible)](https://galaxy.ansible.com/ui/repo/published/kostiantyn_matsebora/barekube/)

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

1. Install [Ansible](https://docs.ansible.com/ansible/latest/installation_guide/) or [Docker](https://docs.docker.com/get-docker/) on your computer, and make sure you can log in to your machines with [SSH](https://www.openssh.com/).

2. Put your machines in `inventory.yml`, the first master first:

   ```yaml
   all:
     children:
       masters:
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

3. Build the cluster, one of two ways (log in with a password instead of a key: add `-k`):

   - Release archive, with Docker or Ansible:

     ```shell
     curl -fsSL https://github.com/kostiantyn-matsebora/barekube/releases/latest/download/barekube-ansible.tar.gz | tar xz
     ./barekube-ansible/play.sh                                   # uses ./inventory.yml
     ./barekube-ansible/play.sh -i ~/clusters/home/inventory.yml  # inventory anywhere
     ```

   - Ansible collection from [Galaxy](https://galaxy.ansible.com/ui/repo/published/kostiantyn_matsebora/barekube/), with Ansible:

     ```shell
     ansible-galaxy collection install kostiantyn_matsebora.barekube
     ansible-playbook -i inventory.yml kostiantyn_matsebora.barekube.site
     ```

4. Use it. The cluster is now the current context in `~/.kube/config`:

   ```shell
   kubectl get nodes
   ```

Add machines or change settings: run step 3 again. [More](orchestration/ansible/README.md). One node by hand: [use the CLI on each node](#using-the-cli).

## Using the CLI

> [!IMPORTANT]
> The CLI works on **one node at a time**: install it and run it **on each machine of the cluster** (over SSH), not on your own computer. To build a whole cluster from your computer, use the [Ansible playbooks](#quick-start-build-a-cluster), which install and run the CLI on every node for you.

### Installation

Do this on each node.

To install the barekube CLI, do the following:

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

#### Verifying the download

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

### Usage

Once installed, you can use barekube CLI by running `barekube --help` command in your terminal. It will show you a help message with available commands:

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

### Running from scripts

```shell
export K3S_TOKEN='<token from the first master>'   # not a flag: stays out of ps and history
barekube install initial-master --yes --tls-san k8s.home.lan --disable traefik
barekube install master-node --yes --server https://192.168.1.10:6443
barekube install agent-node  --yes --server https://192.168.1.10:6443
```

`--yes` skips the questions. Each command ends with `installed`, `updated` or `already installed`. Run again on an installed server with changed `--tls-san`, `--disable` or `--extra-args`, and k3s is reconfigured and restarted (`updated`); with the same ones, nothing happens.

## gVisor as the default

Pods use `runc` unless they set `runtimeClassName: gvisor`. To make gVisor the default:

```shell
barekube install initial-master --gvisor-default   # at install
barekube install gvisor --default                  # running cluster
barekube install gvisor --no-default               # back
```

- Skipped: `kube-system`, `kube-public`, `kube-node-lease`, `longhorn-system`, pods on the host network, PID or IPC namespace, privileged pods
- Opt out: `runtimeClassName: runc`
- Needs: Kubernetes with `MutatingAdmissionPolicy`
