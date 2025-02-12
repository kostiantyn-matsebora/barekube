# Barekube - bare-metal kubernetes  platform

The overall idea is to create CLI for provisioning and configuration of bare-metal kubernetes cluster tailored for home setup, IoT and Edge, including (but not only)
following components:

- [gVisor](https://en.wikipedia.org/wiki/GVisor) - security layer for running containers efficiently and securely.
- [k3](https://k3s.io/) kubernetes cluster with [wireguard](https://www.wireguard.com/) network setup and  optional HA configuration.
- Highly available kubernetes API provided with [HAProxy](https://www.haproxy.org/).
- MetalLB as an internal load balancer (optional).
- Nginx as an internal ingress controller (optional).

using YAML-based specifications like this:
```yaml
- cluster:
    nodes:
      - name: master-1
        role: master
        IP: 192.168.1.10
      - name: master-2
        role: master
        IP: 192.168.1.15
      - name: master-3
        role: master
        IP: 192.168.1.5
      - name: agent-1
        role: agent
        IP: 192.168.1.50
      - name: agent-2
        role: agent
        IP: 192.168.1.30
```

## Installation

To install barekube CLI you simply must do the following steps:

- Download barekube CLI script the destination directory, for instance `$HOME/bin`: 

  ```Bash
    mkdir -p $HOME/bin
    wget https://raw.githubusercontent.com/kostiantyn-matsebora/barekube/refs/heads/gvisor/cli/release/barekube -O $HOME/bin/barekube
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
## Usage

After installation, you can use barekube CLI by running `barekube --help` command in your terminal. It will show you a help message with available commands.
