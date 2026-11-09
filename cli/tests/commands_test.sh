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
load_command install/node-agent barekube_install_node_agent_command
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
    resolve_console_token valid_console_addr valid_address valid_interval valid_token valid_port valid_operations valid_url fetch_node_agent \
    setup_node_agent node_agent_fingerprint remove_node_agent hostname \
    check_if_command_exists k3s_args_changed save_k3s_args echo_info echo_final
  # Not installed unless a test says so.
  mock_fail check_if_command_exists
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
    "check_if_command_exists k3s" \
    "check_k3s_installed " \
    "echo_info Installing initial k3s master node..." \
    "gather_additional_args  " \
    "install_k3s_initial_master --disable=traefik" \
    "save_k3s_args --disable=traefik" \
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

# ---------------------------------------------------------------- node-agent

node_agent_args() {
  args[--console-addr]="http://c:8080"
  args[--heartbeat-interval]="5s"
  mock_output resolve_console_token "tok-0123456789abcdef"
  mock_output hostname "this-host"
  mock_output node_agent_fingerprint "abc123"
  mock_output fetch_node_agent "/work/node-agent-linux-amd64"
  NODE_AGENT_CHANGED=1
}

test_install_node_agent_downloads_and_sets_it_up() {
  node_agent_args
  run barekube_install_node_agent_command
  assert_status 0
  assert_called_matching '^fetch_node_agent /tmp/.* $'
  assert_called "setup_node_agent /work/node-agent-linux-amd64 tok-0123456789abcdef this-host http://c:8080 5s  "
  assert_called "echo_final Failed to install the node-agent. The node-agent runs on this-host."
  assert_output_line "fingerprint abc123"
  assert_called "report_state installed"
}

test_install_node_agent_reports_no_change() {
  node_agent_args
  NODE_AGENT_CHANGED=""
  setup_node_agent() { NODE_AGENT_CHANGED=""; }
  run barekube_install_node_agent_command
  assert_called "report_state already installed"
}

test_install_node_agent_passes_the_options() {
  node_agent_args
  args[--node-name]="n1"
  args[--api-vip]="10.0.0.9"
  args[--api-gateway]="10.0.0.1"
  args[--yes]=1
  args[--skip-signature-check]=1
  run barekube_install_node_agent_command
  assert_called "resolve_console_token --yes"
  assert_called_matching '^fetch_node_agent /tmp/.* --skip-signature-check$'
  assert_called "setup_node_agent /work/node-agent-linux-amd64 tok-0123456789abcdef n1 http://c:8080 5s 10.0.0.9 10.0.0.1"
}

test_install_node_agent_uses_a_given_binary() {
  node_agent_args
  args[--binary]="$TEST_TMP/agent"
  touch "$TEST_TMP/agent"
  run barekube_install_node_agent_command
  assert_status 0
  ! _has_call_matching '^fetch_node_agent'
  assert_called "setup_node_agent $TEST_TMP/agent tok-0123456789abcdef this-host http://c:8080 5s  "
}

test_install_node_agent_rejects_a_missing_binary() {
  node_agent_args
  args[--binary]="$TEST_TMP/nothing"
  run barekube_install_node_agent_command
  assert_status 1
  assert_output_contains "No such file"
  refute_called "setup_node_agent $TEST_TMP/nothing tok-0123456789abcdef this-host http://c:8080 5s  "
}

test_install_node_agent_stops_when_the_download_fails() {
  node_agent_args
  mock_fail fetch_node_agent
  run barekube_install_node_agent_command
  assert_status 1
  refute_called "report_state installed"
}

test_install_node_agent_needs_the_console_address() {
  node_agent_args
  unset 'args[--console-addr]'
  run barekube_install_node_agent_command
  assert_status 1
  assert_output_contains "--console-addr is required"
}

test_install_node_agent_validates_the_inputs() {
  for case in console_addr:valid_console_addr:"--console-addr must look like" \
              node_name:valid_address:"--node-name may only have" \
              interval:valid_interval:"--heartbeat-interval must be" \
              token:valid_token:"The token needs"; do
    node_agent_args
    IFS=: read -r _ validator message <<<"$case"
    mock_fail "$validator"
    run barekube_install_node_agent_command
    assert_status 1
    assert_output_contains "$message"
    unset "MOCK_FAIL_$(_mock_key "$validator")"
  done
}

test_install_node_agent_sets_the_per_node_settings() {
  node_agent_args
  args[--port]="9199"
  args[--operations]="reboot,standby"
  args[--etcd-metrics-url]="http://127.0.0.1:2399/metrics"
  setup_node_agent() { echo "$NODE_AGENT_PORT|$NODE_AGENT_OPERATIONS|$NODE_AGENT_ETCD_METRICS" >>"$CALLS"; }
  run barekube_install_node_agent_command
  assert_status 0
  assert_called "9199|reboot,standby|http://127.0.0.1:2399/metrics"
}

test_install_node_agent_validates_the_per_node_settings() {
  for case in --port:valid_port:"--port must be" \
              --operations:valid_operations:"--operations must list" \
              --etcd-metrics-url:valid_url:"--etcd-metrics-url must be"; do
    node_agent_args
    IFS=: read -r flag validator message <<<"$case"
    args[$flag]="x"
    mock_fail "$validator"
    run barekube_install_node_agent_command
    assert_status 1
    assert_output_contains "$message"
    unset "MOCK_FAIL_$(_mock_key "$validator")"
  done
}

test_install_node_agent_needs_both_vip_values() {
  node_agent_args
  args[--api-vip]="10.0.0.9"
  mock_fail valid_address "10.0.0.1"
  run barekube_install_node_agent_command
  assert_status 1
  assert_output_contains "--api-vip and --api-gateway must be given together"
}

test_install_node_agent_stops_without_a_token() {
  node_agent_args
  mock_fail resolve_console_token
  run barekube_install_node_agent_command
  assert_status 1
  ! _has_call_matching '^setup_node_agent'
}

test_install_node_agent_remove() {
  args[--remove]=1
  run barekube_install_node_agent_command
  assert_status 0
  assert_calls \
    "echo_info Removing the node-agent..." \
    "remove_node_agent " \
    "echo_final Failed to remove the node-agent. The node-agent is removed." \
    "report_state removed"
}

# ------------------------------------------------- changed arguments on an installed node

# check_k3s_installed exits the command when k3s is there and nothing is to be
# redone; the plain mock would let the script go on.
exits_when_installed() {
  check_k3s_installed() { echo "check_k3s_installed $*" >>"$CALLS"; exit 0; }
}

installed_with_flags() {
  unset "MOCK_FAIL_$(_mock_key check_if_command_exists)"
  exits_when_installed
  args[--disable]="'traefik'"
  args[--server]="https://m:6443"
  mock_output resolve_server_url "https://m:6443"
  mock_output resolve_token "t"
  mock_output gather_additional_args "--disable=traefik"
}

test_initial_master_updates_an_installed_node_when_the_arguments_changed() {
  installed_with_flags
  run barekube_install_initial_master_command
  assert_status 0
  assert_calls \
    "check_if_command_exists k3s" \
    "gather_additional_args  " \
    "k3s_args_changed --disable=traefik" \
    "echo_info Installing initial k3s master node..." \
    "install_k3s_initial_master --disable=traefik" \
    "save_k3s_args --disable=traefik" \
    "echo_final Failed to update initial k3s master node. Initial k3s master node updated successfully." \
    "report_state updated"
}

test_initial_master_leaves_an_installed_node_alone_when_the_arguments_are_the_same() {
  installed_with_flags
  mock_fail k3s_args_changed
  run barekube_install_initial_master_command
  assert_called "check_k3s_installed "
  refute_called_matching "install_k3s_initial_master"
  refute_called_matching "save_k3s_args"
}

test_initial_master_leaves_an_installed_node_alone_without_flags() {
  unset "MOCK_FAIL_$(_mock_key check_if_command_exists)"
  exits_when_installed
  run barekube_install_initial_master_command
  assert_called "check_k3s_installed "
  refute_called_matching "k3s_args_changed"
  refute_called_matching "install_k3s_initial_master"
}

test_reinstall_never_takes_the_update_path() {
  installed_with_flags
  mock check_k3s_installed
  args[--reinstall]=1
  run barekube_install_initial_master_command
  assert_called "check_k3s_installed 1"
  refute_called_matching "report_state updated"
  assert_called "report_state installed"
}

test_master_node_updates_an_installed_node_when_the_arguments_changed() {
  installed_with_flags
  run barekube_install_master_node_command
  assert_status 0
  assert_called "install_k3s_master https://m:6443 --disable=traefik"
  assert_called "save_k3s_args --disable=traefik"
  assert_called "report_state updated"
  refute_called_matching "setup_gvisor"
}

test_master_node_leaves_an_installed_node_alone_when_the_arguments_are_the_same() {
  installed_with_flags
  mock_fail k3s_args_changed
  run barekube_install_master_node_command
  assert_called "check_k3s_installed "
  refute_called_matching "install_k3s_master"
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
    "install_k3s_master https://m:6443 " "save_k3s_args " "setup_gvisor " \
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

test_install_gvisor_passes_no_mode_by_default() {
  run barekube_install_gvisor_command
  assert_calls "setup_gvisor "
}

test_install_gvisor_default_flag_turns_the_default_on() {
  args[--default]=1
  run barekube_install_gvisor_command
  assert_calls "setup_gvisor on"
}

test_install_gvisor_no_default_flag_turns_the_default_off() {
  args[--no-default]=1
  run barekube_install_gvisor_command
  assert_calls "setup_gvisor off"
}

# check_gvisor_default_flag COMMAND_FUNCTION
check_gvisor_default_flag() {
  run "$1"
  assert_called "setup_gvisor "
  args[--gvisor-default]=1
  run "$1"
  assert_called "setup_gvisor on"
}

test_initial_master_forwards_the_gvisor_default_flag() { check_gvisor_default_flag barekube_install_initial_master_command; }
test_master_node_forwards_the_gvisor_default_flag() { check_gvisor_default_flag barekube_install_master_node_command; }
test_agent_node_sets_up_gvisor_without_a_default() {
  run barekube_install_agent_node_command
  assert_called "setup_gvisor "
  refute_called_matching "setup_gvisor on"
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
test_upgrade() {
  run barekube_upgrade_command
  assert_status 0
  assert_calls "upgrade_barekube  "
}

test_upgrade_forwards_the_yes_flag() {
  args[--yes]=1
  run barekube_upgrade_command
  assert_called "upgrade_barekube --yes "
}

test_upgrade_without_yes_forwards_nothing() {
  run barekube_upgrade_command
  assert_called "upgrade_barekube  "
}

test_upgrade_forwards_the_skip_signature_check_flag() {
  args[--skip-signature-check]=1
  run barekube_upgrade_command
  assert_called "upgrade_barekube  --skip-signature-check"
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
