#!/usr/bin/env bash
# Tests for the bashly command snippets under commands/: each one must call the
# right library functions in the right order. Library functions are mocked.
source "$(dirname "${BASH_SOURCE[0]}")/lib/testing.sh"
source "$CLI_DIR/src/shared/shared.sh"

load_command install/dependencies barekube_install_dependencies_command
load_command install/initial-master barekube_install_initial_master_command
load_command install/master-node barekube_install_master_node_command
load_command install/agent-node barekube_install_agent_node_command
load_command install/gvisor barekube_install_gvisor_command
load_command install/longhorn-dependencies barekube_install_longhorn_dependencies_command
load_command k3s/start barekube_k3s_start_command
load_command k3s/stop barekube_k3s_stop_command
load_command k3s/start-agent barekube_k3s_start_agent_command
load_command k3s/stop-agent barekube_k3s_stop_agent_command
load_command k3s/killall barekube_k3s_killall_command
load_command k3s/uninstall barekube_k3s_uninstall_command
load_command system/enable-autoupdate barekube_system_enable_autoupdate_command
load_command upgrade barekube_upgrade_command

setup() {
  declare -gA args=()
  mock inspect_args check_k3s_installed gather_master_params gather_additional_args \
    update_packages install_wireguard install_gvisor register_gvisor_to_k3s \
    install_k3s_initial_master install_k3s_master install_k3s_agent show_k3s_token \
    install_longhorn_dependencies start_k3s_master stop_k3s_master start_k3s_agent \
    stop_k3s_agent killall_k3s uninstall_k3s enable_autoupdate upgrade_barekube \
    echo_info echo_final
}

test_install_dependencies() {
  run barekube_install_dependencies_command
  assert_status 0
  assert_calls \
    "inspect_args " \
    "echo_info Installing dependencies..." \
    "update_packages " \
    "install_wireguard " \
    "echo_final Failed to install dependencies. Dependencies are installed succcessfully."
}

test_install_initial_master_without_reinstall() {
  mock_output gather_additional_args "--disable=traefik"
  run barekube_install_initial_master_command
  assert_status 0
  assert_calls \
    "inspect_args " \
    "check_k3s_installed " \
    "echo_info Installing initial k3s master node..." \
    "gather_additional_args " \
    "install_k3s_initial_master --disable=traefik" \
    "install_gvisor " \
    "register_gvisor_to_k3s " \
    "show_k3s_token " \
    "echo_final Failed to install initial k3s master node. Initial k3s master node installed successfully."
}

test_install_initial_master_passes_reinstall_flag() {
  args[--reinstall]=1
  run barekube_install_initial_master_command
  assert_called "check_k3s_installed 1"
}

test_install_master_node() {
  mock_output gather_master_params "--server=https://m:6443 --token=t"
  run barekube_install_master_node_command
  assert_status 0
  assert_called "check_k3s_installed "
  assert_called "install_k3s_master --server=https://m:6443 --token=t "
  assert_called "echo_info Installing master node..."
  assert_called "echo_info Master parameters: --server=https://m:6443 --token=t"
  assert_calls_in_order \
    "inspect_args " "check_k3s_installed " "gather_master_params " "gather_additional_args " \
    "install_k3s_master --server=https://m:6443 --token=t " "install_gvisor " \
    "register_gvisor_to_k3s " \
    "echo_final Failed to install k3s master node. Another k3s master node installed successfully."
}

test_install_master_node_forwards_additional_arguments_and_reinstall() {
  args[--reinstall]=1
  mock_output gather_master_params "--server=u --token=t"
  mock_output gather_additional_args "--tls-san x"
  run barekube_install_master_node_command
  assert_called "check_k3s_installed 1"
  assert_called "install_k3s_master --server=u --token=t --tls-san x"
}

test_install_agent_node() {
  mock_output gather_master_params "--server=https://m:6443 --token=t"
  run barekube_install_agent_node_command
  assert_status 0
  assert_calls \
    "inspect_args " \
    "check_k3s_installed " \
    "gather_master_params " \
    "echo_info Installing agent node..." \
    "install_k3s_agent --server=https://m:6443 --token=t" \
    "install_gvisor " \
    "register_gvisor_to_k3s " \
    "echo_final Failed to install k3s agent node. Another k3s agent node installed successfully."
}

test_install_agent_node_passes_reinstall_flag() {
  args[--reinstall]=1
  run barekube_install_agent_node_command
  assert_called "check_k3s_installed 1"
}

test_install_gvisor() {
  run barekube_install_gvisor_command
  assert_status 0
  assert_calls "inspect_args " "install_gvisor " "register_gvisor_to_k3s "
}

test_install_longhorn_dependencies() {
  run barekube_install_longhorn_dependencies_command
  assert_status 0
  assert_called "inspect_args "
  assert_called "echo_info Installing longhorn dependencies..."
  assert_called "install_longhorn_dependencies "
  assert_called "echo_final Failed to install longhorn dependencies. Longhorn dependencies are installed succcessfully."
  assert_called_matching "^echo_info To fix multipath issue, please follow instructions: .*https://longhorn.io/kb/troubleshooting-volume-with-multipath/"
}

# check_delegate COMMAND_FUNCTION LIBRARY_FUNCTION
check_delegate() {
  run "$1"
  assert_status 0
  assert_calls "inspect_args " "$2 "
}

test_k3s_start() { check_delegate barekube_k3s_start_command start_k3s_master; }
test_k3s_stop() { check_delegate barekube_k3s_stop_command stop_k3s_master; }
test_k3s_start_agent() { check_delegate barekube_k3s_start_agent_command start_k3s_agent; }
test_k3s_stop_agent() { check_delegate barekube_k3s_stop_agent_command stop_k3s_agent; }
test_k3s_killall() { check_delegate barekube_k3s_killall_command killall_k3s; }
test_k3s_uninstall() { check_delegate barekube_k3s_uninstall_command uninstall_k3s; }
test_system_enable_autoupdate() { check_delegate barekube_system_enable_autoupdate_command enable_autoupdate; }
test_upgrade() { check_delegate barekube_upgrade_command upgrade_barekube; }

test_upgrade_forwards_the_yes_flag() {
  args[--yes]=1
  run barekube_upgrade_command
  assert_called "upgrade_barekube --yes"
}

test_upgrade_without_yes_forwards_nothing() {
  run barekube_upgrade_command
  assert_called "upgrade_barekube "
}

# A failed parameter prompt (gather_master_params returns 1) must stop the install.
check_aborts_on_missing_params() {
  mock_fail gather_master_params
  run "$1"
  assert_status 1
  refute_called_matching '^install_'
}

test_master_node_aborts_when_master_params_are_missing() {
  check_aborts_on_missing_params barekube_install_master_node_command
}

test_agent_node_aborts_when_master_params_are_missing() {
  check_aborts_on_missing_params barekube_install_agent_node_command
}

test_every_command_prints_its_banner_before_running() {
  run barekube_k3s_start_command
  assert_output_contains "# It contains the implementation for the 'barekube k3s start' command."
}

run_tests
