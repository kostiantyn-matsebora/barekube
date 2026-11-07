#!/usr/bin/env bash
# End-to-end tests of the generated release/barekube script. It runs as a real
# subprocess with stub executables first on PATH, so no system is modified:
# every sudo/apt/curl/... invocation is recorded in $CALLS instead.
source "$(dirname "${BASH_SOURCE[0]}")/lib/testing.sh"

RELEASE="$CLI_DIR/release/barekube"
VERSION="$(sed -n 's/^version: *//p' "$CLI_DIR/bashly.yml" | tr -d '\r')"

# make_stub NAME [EXIT_CODE] [STDOUT]
make_stub() {
  cat >"$STUBS/$1" <<STUB
#!/bin/sh
echo "$1 \$*" >>"$CALLS"
[ -z "$3" ] || echo "$3"
exit ${2:-0}
STUB
  chmod +x "$STUBS/$1"
}

setup() {
  STUBS="$TEST_TMP/stubs"
  mkdir -p "$STUBS" "$TEST_TMP/home/bin"
  HOME="$TEST_TMP/home"
  # /usr/local/bin is left out so a real k3s install cannot leak into the tests.
  PATH="$STUBS:/usr/bin:/bin"
  export HOME PATH
  make_stub sudo
  make_stub curl
  make_stub sh
  make_stub cp
  make_stub grep 1
  make_stub dpkg 0 amd64
  make_stub barekube 0 "$VERSION"
  cat >"$STUBS/wget" <<STUB
#!/bin/sh
echo "wget \$*" >>"$CALLS"
while [ \$# -gt 0 ]; do [ "\$1" = "-O" ] && : >"\$2"; shift; done
STUB
  chmod +x "$STUBS/wget"
}

# Runs a command with only the stub directory on PATH (no wget available).
run_without_path() { PATH="$STUBS" "$@"; }
barekube_cli() { bash "$RELEASE" "$@"; }

# ------------------------------------------------------- version & help

test_version_flags_print_the_configured_version() {
  [[ -n "$VERSION" ]] || fail "could not read version from bashly.yml"
  run barekube_cli --version
  assert_status 0
  assert_eq "$VERSION" "$output"
  run barekube_cli -v
  assert_eq "$VERSION" "$output"
}

test_help_lists_every_command() {
  local cmd
  run barekube_cli --help
  assert_status 0
  output="$(strip_ansi <<<"$output")"
  assert_output_contains "barekube - CLI that provides functionality for installing"
  for cmd in "install dependencies" "install initial-master" "install master-node" \
    "install agent-node" "install gvisor" "install longhorn-dependencies" \
    "k3s start" "k3s stop" "k3s uninstall" "k3s killall" "k3s start-agent" \
    "k3s stop-agent" "system enable-autoupdate" "upgrade"; do
    assert_output_contains "$cmd"
  done
  assert_output_contains "MIT License"
}

test_short_help_flag_works() {
  run barekube_cli -h
  assert_status 0
  assert_output_contains "Usage:"
}

test_no_arguments_prints_usage_and_fails() {
  run barekube_cli
  assert_status 1
  assert_output_contains "Usage:"
}

test_group_commands_print_their_usage() {
  local group
  for group in install k3s system; do
    run barekube_cli "$group"
    assert_status 1
    assert_output_contains "barekube $group COMMAND"
  done
}

test_every_command_has_help() {
  local cmd
  for cmd in "install dependencies" "install initial-master" "install master-node" \
    "install agent-node" "install gvisor" "install longhorn-dependencies" \
    "k3s start" "k3s stop" "k3s uninstall" "k3s killall" "k3s start-agent" \
    "k3s stop-agent" "system enable-autoupdate" "upgrade"; do
    # shellcheck disable=SC2086
    run barekube_cli $cmd --help
    assert_status 0
    assert_output_contains "barekube $cmd"
  done
}

test_install_commands_document_the_reinstall_flag() {
  local cmd
  for cmd in initial-master master-node agent-node; do
    run barekube_cli install "$cmd" --help
    assert_output_contains "--reinstall"
  done
}

# ------------------------------------------------------ argument errors

test_unknown_command_is_rejected() {
  run barekube_cli bogus
  assert_status 1
  assert_output_contains "invalid command: bogus"
}

test_unknown_option_is_rejected() {
  run barekube_cli install initial-master --bogus
  assert_status 1
  assert_output_contains "invalid option: --bogus"
}

test_unexpected_argument_is_rejected() {
  run barekube_cli k3s start extra
  assert_status 1
  assert_output_contains "invalid argument: extra"
}

test_unknown_subcommand_is_rejected() {
  run barekube_cli k3s bogus
  assert_status 1
  assert_output_contains "invalid"
}

test_external_barekube_subcommands_are_executed() {
  make_stub barekube-plugin 0 "plugin ran"
  run barekube_cli plugin --flag value
  assert_status 0
  assert_output_contains "plugin ran"
  assert_called "barekube-plugin --flag value"
}

test_upgrade_fails_without_wget() {
  rm "$STUBS/wget"
  run run_without_path "$BASH" "$RELEASE" upgrade
  assert_status 1
  assert_output_contains "missing dependency: wget"
}

# ------------------------------------------------------- k3s commands

test_k3s_start_and_stop_control_the_service() {
  run barekube_cli k3s start
  assert_status 0
  run barekube_cli k3s stop
  assert_status 0
  run barekube_cli k3s start-agent
  assert_status 0
  run barekube_cli k3s stop-agent
  assert_status 0
  assert_calls \
    "sudo systemctl start k3s" \
    "sudo systemctl stop k3s" \
    "sudo systemctl start k3s-agent" \
    "sudo systemctl stop k3s-agent"
}

test_k3s_killall_runs_the_killall_script() {
  run barekube_cli k3s killall
  assert_status 0
  assert_calls "sudo /usr/local/bin/k3s-killall.sh"
}

test_k3s_uninstall_asks_before_uninstalling() {
  run_input "y" barekube_cli k3s uninstall
  assert_status 0
  assert_calls "sudo /usr/local/bin/k3s-uninstall.sh"
}

test_k3s_uninstall_with_yes_does_not_ask() {
  run barekube_cli k3s uninstall --yes
  assert_status 0
  assert_calls "sudo /usr/local/bin/k3s-uninstall.sh"
}

test_k3s_uninstall_can_be_declined() {
  run_input "n" barekube_cli k3s uninstall
  assert_status 0
  assert_no_calls
}

# --------------------------------------------------- install commands

test_install_dependencies() {
  run barekube_cli install dependencies
  assert_status 0
  assert_calls "sudo apt update" "sudo apt install -y wireguard"
  assert_output_contains "Dependencies are installed succcessfully."
}

test_install_longhorn_dependencies() {
  run barekube_cli install longhorn-dependencies
  assert_status 0
  assert_called "sudo apt install -y open-iscsi"
  assert_called "sudo apt install -y nfs-common"
  assert_output_contains "Longhorn dependencies are installed succcessfully."
  assert_output_contains "https://longhorn.io/kb/troubleshooting-volume-with-multipath/"
}

test_install_gvisor_installs_and_registers_the_runtime() {
  run barekube_cli install gvisor
  assert_status 0
  assert_called "sudo apt-get install -y runsc"
  assert_called "sudo systemctl restart k3s"
}

test_install_initial_master_without_extra_options() {
  make_stub sudo 0 "node-token"
  run_input "n" barekube_cli install initial-master --reinstall
  assert_status 0
  assert_called "sh -s - server --cluster-init --flannel-backend wireguard-native"
  assert_called "sudo systemctl restart k3s"
  assert_output_contains "node-token"
  assert_output_contains "Initial k3s master node installed successfully."
}

# A stand-in for `sh` that also records the join variables the installer reads.
make_installer_stub() {
  cat >"$STUBS/sh" <<STUB
#!/bin/sh
echo "sh \$*" >>"$CALLS"
echo "env K3S_TOKEN=\$K3S_TOKEN K3S_URL=\$K3S_URL" >>"$CALLS"
STUB
  chmod +x "$STUBS/sh"
}

test_install_master_node_joins_the_cluster() {
  make_installer_stub
  run_input $'https://10.0.0.1:6443\nsecret\nn' barekube_cli install master-node --reinstall
  assert_status 0
  assert_called "sh -s - server --server https://10.0.0.1:6443 --flannel-backend wireguard-native"
  assert_called "env K3S_TOKEN=secret K3S_URL="
  assert_output_contains "Another k3s master node installed successfully."
}

test_install_agent_node_joins_the_cluster() {
  make_installer_stub
  run_input $'https://10.0.0.1:6443\nsecret' barekube_cli install agent-node --reinstall
  assert_status 0
  assert_called "sh -s - agent"
  assert_called "env K3S_TOKEN=secret K3S_URL=https://10.0.0.1:6443"
  assert_output_contains "Another k3s agent node installed successfully."
}

# ------------------------------------------ non-interactive installs (stdin is empty)

test_install_initial_master_with_yes_never_prompts() {
  make_stub sudo 0 "node-token"
  run barekube_cli install initial-master --reinstall --yes
  assert_status 0
  assert_called "sh -s - server --cluster-init --flannel-backend wireguard-native"
  assert_output_line "installed"
}

test_install_initial_master_takes_k3s_options_from_flags() {
  run barekube_cli install initial-master --reinstall --yes \
    --tls-san k8s.home.lan --tls-san 192.168.1.100 --disable servicelb --disable traefik \
    --extra-args=--node-label=zone=a
  assert_status 0
  assert_called "sh -s - server --cluster-init --flannel-backend wireguard-native --disable=servicelb --disable=traefik --tls-san k8s.home.lan --tls-san 192.168.1.100 --node-label=zone=a"
}

test_install_master_node_is_fully_driven_by_flags_and_environment() {
  make_installer_stub
  K3S_TOKEN=s3cret run barekube_cli install master-node --reinstall --yes \
    --server https://192.168.1.100:6443 --tls-san k8s.home.lan --disable traefik
  assert_status 0
  assert_called "sh -s - server --server https://192.168.1.100:6443 --flannel-backend wireguard-native --disable=traefik --tls-san k8s.home.lan"
  assert_called "env K3S_TOKEN=s3cret K3S_URL="
  assert_output_line "installed"
}

test_install_agent_node_is_fully_driven_by_flags_and_environment() {
  make_installer_stub
  K3S_TOKEN=s3cret run barekube_cli install agent-node --reinstall --yes --server https://192.168.1.100:6443
  assert_status 0
  assert_called "sh -s - agent"
  assert_called "env K3S_TOKEN=s3cret K3S_URL=https://192.168.1.100:6443"
  assert_output_line "installed"
}

test_the_join_token_never_reaches_the_output_or_a_command_line() {
  K3S_TOKEN=s3cret run barekube_cli install agent-node --reinstall --yes --server https://192.168.1.100:6443
  refute_output_contains "s3cret"
  refute_called_matching "s3cret"
  K3S_TOKEN=s3cret run barekube_cli install master-node --reinstall --yes --server https://192.168.1.100:6443
  refute_output_contains "s3cret"
  refute_called_matching "s3cret"
}

test_install_with_yes_fails_without_a_server_instead_of_prompting() {
  K3S_TOKEN=s3cret run barekube_cli install agent-node --reinstall --yes
  assert_status 1
  assert_output_contains "--server is required with --yes."
  refute_called_matching '^sh '
}

test_install_with_yes_fails_without_a_token_instead_of_prompting() {
  unset K3S_TOKEN
  run barekube_cli install master-node --reinstall --yes --server https://192.168.1.100:6443
  assert_status 1
  assert_output_contains "K3S_TOKEN is required with --yes."
  refute_called_matching '^sh '
}

test_install_commands_document_the_non_interactive_options() {
  run barekube_cli install master-node --help
  assert_output_contains "--server URL"
  assert_output_contains "--yes, -y"
  assert_output_contains "--tls-san SAN (repeatable)"
  assert_output_contains "--disable COMPONENT (repeatable)"
  assert_output_contains "--extra-args ARGUMENTS"
  assert_output_contains "K3S_TOKEN"
  run barekube_cli install agent-node --help
  assert_output_contains "K3S_TOKEN"
}

test_command_output_has_no_bashly_boilerplate() {
  run barekube_cli k3s start
  refute_output_contains "It contains the implementation"
  refute_output_contains "args:"
}

test_install_exits_early_when_k3s_is_already_installed() {
  make_stub k3s
  run barekube_cli install initial-master
  assert_status 0
  assert_output_contains "k3s is already installed."
  assert_output_line "already installed"
  refute_called_matching '^sh '
}

test_install_master_node_reports_a_missing_master_url() {
  run_input $'\n' barekube_cli install master-node --reinstall
  assert_output_contains "Master URL is required."
}

test_install_master_node_aborts_without_a_master_url() {
  # Regression: gather_master_params exited inside $(...), which only left the
  # substitution, so the error text was spliced into the k3s install command.
  run_input $'\n' barekube_cli install master-node --reinstall
  [[ "$status" -ne 0 ]] || fail "exit status was 0"
  refute_called_matching '^sh '
}

# ----------------------------------------------- system & upgrade commands

test_system_enable_autoupdate() {
  run barekube_cli system enable-autoupdate
  assert_status 0
  assert_calls \
    "sudo apt update" \
    "sudo apt install -y unattended-upgrades" \
    "sudo dpkg-reconfigure --priority=low unattended-upgrades" \
    "sudo nano /etc/apt/apt.conf.d/50unattended-upgrades"
}

test_upgrade_downloads_the_latest_release() {
  run_input $'\n' barekube_cli upgrade --yes
  assert_status 0
  assert_called "wget https://raw.githubusercontent.com/kostiantyn-matsebora/barekube/master/cli/release/barekube -O $HOME/bin/barekube"
  [[ -x "$HOME/bin/barekube" ]] || fail "downloaded CLI is not executable"
  assert_output_contains "upgraded successfully to version $VERSION"
}

test_upgrade_short_flag_is_accepted() {
  run_input $'\n' barekube_cli upgrade -y
  assert_status 0
}

test_upgrade_yes_flag_skips_the_path_prompt() {
  # Regression: the upgrade command did not forward --yes, so the user was
  # still prompted for a path.
  run_input "/opt" barekube_cli upgrade --yes
  assert_status 0
  assert_called "wget https://raw.githubusercontent.com/kostiantyn-matsebora/barekube/master/cli/release/barekube -O $HOME/bin/barekube"
}

run_tests
