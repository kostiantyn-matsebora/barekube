# barekube role

Installs the [barekube](https://github.com/kostiantyn-matsebora/barekube) CLI on a node and runs it to build, change or remove a bare-metal k3s cluster. The playbooks of this collection (`site`, `remove_node`, `uninstall`) use it; the task files are the steps.

Settings are the `barekube_*` variables in [`defaults/main.yml`](defaults/main.yml), documented in the [collection README](../../README.md#settings).
