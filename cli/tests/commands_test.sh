#!/usr/bin/env bash
# Tests for the bashly command snippets under commands/: each one must call the
# right library functions in the right order. Library functions are mocked.
source "$(dirname "${BASH_SOURCE[0]}")/lib/testing.sh"
source "$CLI_DIR/src/shared/colors.sh"
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
  mock check_k3s_installed resolve_server_url resolve_token gather_additional_args report_state \
    update_packages install_wireguard setup_gvisor \
    install_k3s_initial_master install_k3s_master install_k3s_agent show_k3s_token \
    install_longhorn_dependencies start_k3s_master stop_k3s_master start_k3s_agent \
    stop_k3s_agent killall_k3s uninstall_k3s enable_autoupdate upgrade_barekube \
    echo_info echo_final
}

test_install_dependencies() {
  run barekube_install_dependencies_command
  assert_status 0
  assert_calls \
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
    "check_k3s_installed " \
    "echo_info Installing initial k3s master node..." \
    "gather_additional_args  " \
    "install_k3s_initial_master --disable=traefik" \
    "setup_gvisor " \
    "show_k3s_token " \
    "echo_final Failed to install initial k3s master node. Initial k3s master node installed successfully." \
    "report_state installed"
}

test_install_initial_master_passes_reinstall_flag() {
  args[--reinstall]=1
  run barekube_install_initial_master_command
  assert_called "check_k3s_installed 1"
}

test_install_initial_master_forwards_yes_and_extra_args() {
  args[--yes]=1
  args[--extra-args]="--node-label=a=b"
  run barekube_install_initial_master_command
  assert_called "gather_additional_args --yes --node-label=a=b"
}

test_install_initial_master_reads_the_repeatable_flags() {
  args[--tls-san]="'a.example' '10.0.0.1'"
  args[--disable]="'servicelb' 'traefik'"
  gather_additional_args() { echo "${TLS_SANS[*]}|${DISABLE_COMPONENTS[*]}"; }
  mock_output_fn() { :; }
  run barekube_install_initial_master_command
  assert_called "install_k3s_initial_master a.example 10.0.0.1|servicelb traefik"
}

test_repeatable_flag_values_with_spaces_stay_whole() {
  args[--tls-san]="'a b' 'c'"
  gather_additional_args() { echo "${#TLS_SANS[@]}:${TLS_SANS[0]}"; }
  run barekube_install_initial_master_command
  assert_called "install_k3s_initial_master 2:a b"
}

test_install_master_node() {
  mock_output resolve_server_url "https://m:6443"
  mock_output resolve_token "t"
  run barekube_install_master_node_command
  assert_status 0
  assert_called "check_k3s_installed "
  assert_called "install_k3s_master https://m:6443 "
  assert_called "echo_info Installing master node..."
  assert_called "echo_info Joining cluster at: https://m:6443"
  assert_calls_in_order \
    "check_k3s_installed " "resolve_server_url  " "resolve_token " "gather_additional_args  " \
    "install_k3s_master https://m:6443 " "setup_gvisor " \
    "echo_final Failed to install k3s master node. Another k3s master node installed successfully." \
    "report_state installed"
}

test_install_master_node_forwards_flags() {
  args[--reinstall]=1
  args[--server]="https://s:6443"
  args[--yes]=1
  args[--extra-args]="--x"
  mock_output resolve_server_url "https://s:6443"
  mock_output gather_additional_args "--tls-san x"
  run barekube_install_master_node_command
  assert_called "check_k3s_installed 1"
  assert_called "resolve_server_url https://s:6443 --yes"
  assert_called "resolve_token --yes"
  assert_called "gather_additional_args --yes --x"
  assert_called "install_k3s_master https://s:6443 --tls-san x"
}

test_install_master_node_exports_the_token_for_the_installer() {
  mock_output resolve_token "s3cret"
  install_k3s_master() { echo "K3S_TOKEN=$K3S_TOKEN" >>"$CALLS"; }
  run barekube_install_master_node_command
  assert_called "K3S_TOKEN=s3cret"
}

test_install_master_node_never_prints_the_token() {
  mock_output resolve_token "s3cret"
  run barekube_install_master_node_command
  refute_called_matching "s3cret"
}

test_install_agent_node() {
  mock_output resolve_server_url "https://m:6443"
  mock_output resolve_token "t"
  run barekube_install_agent_node_command
  assert_status 0
  assert_calls \
    "check_k3s_installed " \
    "resolve_server_url  " \
    "resolve_token " \
    "echo_info Installing agent node..." \
    "install_k3s_agent https://m:6443" \
    "setup_gvisor " \
    "echo_final Failed to install k3s agent node. Another k3s agent node installed successfully." \
    "report_state installed"
}

test_install_agent_node_passes_reinstall_and_yes() {
  args[--reinstall]=1
  args[--yes]=1
  args[--server]="https://s:6443"
  run barekube_install_agent_node_command
  assert_called "check_k3s_installed 1"
  assert_called "resolve_server_url https://s:6443 --yes"
  assert_called "resolve_token --yes"
}

test_install_agent_node_exports_the_token_for_the_installer() {
  mock_output resolve_token "s3cret"
  install_k3s_agent() { echo "K3S_TOKEN=$K3S_TOKEN" >>"$CALLS"; }
  run barekube_install_agent_node_command
  assert_called "K3S_TOKEN=s3cret"
}

test_install_gvisor() {
  run barekube_install_gvisor_command
  assert_status 0
  assert_calls "setup_gvisor "
}

test_install_longhorn_dependencies() {
  run barekube_install_longhorn_dependencies_command
  assert_status 0
  assert_called "echo_info Installing longhorn dependencies..."
  assert_called "install_longhorn_dependencies "
  assert_called "echo_final Failed to install longhorn dependencies. Longhorn dependencies are installed succcessfully."
  assert_called_matching "^echo_info To fix multipath issue, please follow instructions: .*https://longhorn.io/kb/troubleshooting-volume-with-multipath/"
}

# check_delegate COMMAND_FUNCTION LIBRARY_FUNCTION
check_delegate() {
  run "$1"
  assert_status 0
  assert_calls "$2 "
}

test_k3s_start() { check_delegate barekube_k3s_start_command start_k3s_master; }
test_k3s_stop() { check_delegate barekube_k3s_stop_command stop_k3s_master; }
test_k3s_start_agent() { check_delegate barekube_k3s_start_agent_command start_k3s_agent; }
test_k3s_stop_agent() { check_delegate barekube_k3s_stop_agent_command stop_k3s_agent; }
test_k3s_killall() { check_delegate barekube_k3s_killall_command killall_k3s; }
test_k3s_uninstall() { check_delegate barekube_k3s_uninstall_command uninstall_k3s; }

test_k3s_uninstall_forwards_the_yes_flag() {
  args[--yes]=1
  run barekube_k3s_uninstall_command
  assert_called "uninstall_k3s --yes"
}
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

# A missing server URL or token must stop the install before anything runs.
check_aborts_when() {
  mock_fail "$1"
  run "$2"
  assert_status 1
  refute_called_matching '^install_'
  refute_called "setup_gvisor "
}

test_master_node_aborts_when_the_server_url_is_missing() {
  check_aborts_when resolve_server_url barekube_install_master_node_command
}

test_master_node_aborts_when_the_token_is_missing() {
  check_aborts_when resolve_token barekube_install_master_node_command
}

test_agent_node_aborts_when_the_server_url_is_missing() {
  check_aborts_when resolve_server_url barekube_install_agent_node_command
}

test_agent_node_aborts_when_the_token_is_missing() {
  check_aborts_when resolve_token barekube_install_agent_node_command
}

test_commands_do_not_print_bashly_boilerplate() {
  local file
  for file in "$CLI_DIR"/commands/*.sh "$CLI_DIR"/commands/*/*.sh; do
    [[ "$(<"$file")" != *"inspect_args"* ]] || fail "${file#"$CLI_DIR"/} calls inspect_args"
    [[ "$(<"$file")" != *'echo "# '* ]] || fail "${file#"$CLI_DIR"/} echoes the bashly banner"
  done
}

run_tests
