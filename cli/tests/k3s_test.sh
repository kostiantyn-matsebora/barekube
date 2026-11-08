#!/usr/bin/env bash
# Tests for src/k3s.sh. External commands are replaced by logging mocks.
source "$(dirname "${BASH_SOURCE[0]}")/lib/testing.sh"
source "$CLI_DIR/src/shared/colors.sh"
source "$CLI_DIR/src/shared/shared.sh"
source "$CLI_DIR/src/k3s.sh"

setup() {
  mock sudo curl sh check_if_command_exists
  K3S_SYSTEMD_DIR="$TEST_TMP/systemd"
  mkdir -p "$K3S_SYSTEMD_DIR"
}

# ---------------------------------------------------------- already-installed

test_check_k3s_installed_continues_when_k3s_is_missing() {
  mock_fail check_if_command_exists
  run check_k3s_installed ""
  assert_status 0
  assert_calls "check_if_command_exists k3s"
}

test_check_k3s_installed_exits_when_installed_without_reinstall() {
  check_k3s_wrapper() { check_k3s_installed "$1"; echo "continued"; }
  run check_k3s_wrapper ""
  assert_status 0
  assert_output_contains "k3s is already installed."
  assert_output_line "already installed"
  refute_output_contains "continued"
}

test_check_k3s_installed_continues_when_reinstall_requested() {
  check_k3s_wrapper() { check_k3s_installed "$1"; echo "continued"; }
  run check_k3s_wrapper 1
  assert_status 0
  assert_output_contains "k3s is already installed."
  assert_output_contains "continued"
}

# ------------------------------------------------- remembering the arguments

test_normalize_args_collapses_whitespace() {
  run normalize_args "  --disable=traefik   --tls-san  a "
  assert_eq "--disable=traefik --tls-san a" "$output"
}

test_k3s_args_changed_is_true_when_nothing_was_remembered() {
  K3S_ARGS_FILE="$TEST_TMP/none"
  run k3s_args_changed "--disable=traefik"
  assert_status 0
}

test_k3s_args_changed_is_false_for_the_same_arguments() {
  K3S_ARGS_FILE="$TEST_TMP/args"
  echo "--disable=traefik --tls-san a" >"$K3S_ARGS_FILE"
  run k3s_args_changed "  --disable=traefik   --tls-san a"
  assert_status 1
}

test_k3s_args_changed_is_true_for_other_arguments() {
  K3S_ARGS_FILE="$TEST_TMP/args"
  echo "--disable=traefik" >"$K3S_ARGS_FILE"
  run k3s_args_changed "--disable=traefik --disable=servicelb"
  assert_status 0
}

test_save_k3s_args_writes_the_normalized_arguments() {
  K3S_ARGS_FILE="$TEST_TMP/etc/args"
  sudo() { "$@"; }
  run save_k3s_args " --disable=traefik   --tls-san a"
  assert_status 0
  assert_eq "--disable=traefik --tls-san a" "$(cat "$K3S_ARGS_FILE")"
}

# ------------------------------------------------------- gathering parameters

test_gather_additional_args_returns_nothing_when_declined() {
  script_fn question n
  run gather_additional_args
  assert_status 0
  assert_eq "" "$output"
  assert_calls "question Do you want to set additional parameters? [y/n]"
}

test_gather_additional_args_collects_every_option() {
  script_fn question y y y y y
  script_fn read_input my.san --extra=1
  run gather_additional_args
  assert_status 0
  assert_eq " --disable=servicelb --disable=traefik --tls-san my.san --extra=1" "$output"
}

test_gather_additional_args_can_disable_only_the_load_balancer() {
  script_fn question y y n n n
  run gather_additional_args
  assert_eq " --disable=servicelb" "$output"
}

test_gather_additional_args_can_disable_only_traefik() {
  script_fn question y n y n n
  run gather_additional_args
  assert_eq " --disable=traefik" "$output"
}

test_gather_additional_args_can_set_only_tls_san() {
  script_fn question y n n y n
  script_fn read_input 10.0.0.1
  run gather_additional_args
  assert_eq " --tls-san 10.0.0.1" "$output"
}

test_gather_additional_args_can_add_only_free_form_arguments() {
  script_fn question y n n n y
  script_fn read_input --node-label=a=b
  run gather_additional_args
  assert_eq " --node-label=a=b" "$output"
}

test_gather_additional_args_yields_nothing_when_every_option_is_declined() {
  script_fn question y n n n n
  run gather_additional_args
  assert_eq "" "$output"
}

test_gather_additional_args_uses_flag_values_without_prompting() {
  TLS_SANS=(a.example 10.0.0.1)
  DISABLE_COMPONENTS=(servicelb traefik)
  script_fn question
  run gather_additional_args "" "--node-label=x=y"
  assert_status 0
  assert_eq " --disable=servicelb --disable=traefik --tls-san a.example --tls-san 10.0.0.1 --node-label=x=y" "$output"
  assert_no_calls
}

test_gather_additional_args_accepts_only_extra_arguments() {
  run gather_additional_args "" "--node-label=x=y"
  assert_eq " --node-label=x=y" "$output"
}

test_gather_additional_args_accepts_only_a_tls_san_flag() {
  TLS_SANS=(k8s.home.lan)
  run gather_additional_args ""
  assert_eq " --tls-san k8s.home.lan" "$output"
}

test_gather_additional_args_with_yes_and_no_flags_adds_nothing_and_never_asks() {
  script_fn question
  run gather_additional_args --yes
  assert_status 0
  assert_eq "" "$output"
  assert_no_calls
}

test_gather_additional_args_flags_win_over_yes() {
  DISABLE_COMPONENTS=(traefik)
  run gather_additional_args --yes
  assert_eq " --disable=traefik" "$output"
}

test_resolve_server_url_prefers_the_flag_value() {
  script_fn read_input
  run resolve_server_url "https://10.0.0.1:6443" ""
  assert_status 0
  assert_eq "https://10.0.0.1:6443" "$output"
  assert_no_calls
}

test_resolve_server_url_prompts_when_the_flag_is_absent() {
  script_fn read_input https://10.0.0.2:6443
  run resolve_server_url "" ""
  assert_status 0
  assert_eq "https://10.0.0.2:6443" "$output"
}

test_resolve_server_url_requires_a_value() {
  script_fn read_input ""
  run resolve_server_url "" ""
  assert_status 1
  assert_output_contains "Master URL is required."
}

test_resolve_server_url_fails_instead_of_prompting_with_yes() {
  script_fn read_input
  run resolve_server_url "" --yes
  assert_status 1
  assert_output_contains "--server is required with --yes."
  assert_no_calls
}

test_resolve_token_reads_the_environment_variable() {
  script_fn read_input
  K3S_TOKEN=s3cret run resolve_token ""
  assert_status 0
  assert_eq "s3cret" "$output"
  assert_no_calls
}

test_resolve_token_prompts_when_the_variable_is_unset() {
  unset K3S_TOKEN
  script_fn read_input s3cret
  run resolve_token ""
  assert_status 0
  assert_eq "s3cret" "$output"
}

test_resolve_token_requires_a_value() {
  unset K3S_TOKEN
  script_fn read_input ""
  run resolve_token ""
  assert_status 1
  assert_output_contains "Token is required."
}

test_resolve_token_fails_instead_of_prompting_with_yes() {
  unset K3S_TOKEN
  script_fn read_input
  run resolve_token --yes
  assert_status 1
  assert_output_contains "K3S_TOKEN is required with --yes."
  assert_no_calls
}

# -------------------------------------------------------------- k3s install

test_install_k3s_initial_master_pipes_the_installer_into_sh() {
  run install_k3s_initial_master ""
  assert_status 0
  assert_called "curl -sfL https://get.k3s.io"
  assert_called "sh -s - server --cluster-init --flannel-backend wireguard-native"
  assert_output_contains "Running command: curl -sfL https://get.k3s.io | sh -s - server"
}

test_install_k3s_initial_master_reports_failure() {
  mock_fail sh
  run install_k3s_initial_master ""
  assert_output_contains "Failed to install k3s"
}

test_install_k3s_initial_master_forwards_additional_arguments() {
  # Regression: the extra arguments were read from $2 while the command passes
  # them as $1, so --disable/--tls-san choices were silently dropped.
  run install_k3s_initial_master "--disable=traefik"
  assert_called "sh -s - server --cluster-init --flannel-backend wireguard-native --disable=traefik"
}

test_install_k3s_master_joins_the_server_with_additional_arguments() {
  run install_k3s_master "https://m:6443" "--disable=traefik"
  assert_status 0
  assert_called "sh -s - server --server https://m:6443 --flannel-backend wireguard-native --disable=traefik"
}

test_install_k3s_master_reports_failure() {
  mock_fail sh
  run install_k3s_master "https://m:6443" ""
  assert_output_contains "Failed to install k3s"
}

test_install_k3s_agent_joins_the_server() {
  run install_k3s_agent "https://m:6443"
  assert_status 0
  assert_called "curl -sfL https://get.k3s.io"
  assert_called "sh -s - agent"
}

test_install_k3s_agent_passes_the_server_url_in_the_environment() {
  sh() { echo "sh K3S_URL=$K3S_URL K3S_TOKEN=$K3S_TOKEN" >>"$CALLS"; }
  K3S_TOKEN=s3cret run install_k3s_agent "https://m:6443"
  assert_called "sh K3S_URL=https://m:6443 K3S_TOKEN=s3cret"
}

test_install_k3s_master_reaches_the_installer_with_the_token_in_the_environment() {
  sh() { echo "sh K3S_TOKEN=$K3S_TOKEN" >>"$CALLS"; }
  K3S_TOKEN=s3cret run install_k3s_master "https://m:6443" ""
  assert_called "sh K3S_TOKEN=s3cret"
}

test_the_join_token_never_appears_on_a_command_line_or_in_output() {
  K3S_TOKEN=s3cret run install_k3s_agent "https://m:6443"
  refute_output_contains "s3cret"
  refute_called_matching "s3cret"
  K3S_TOKEN=s3cret run install_k3s_master "https://m:6443" ""
  refute_output_contains "s3cret"
  refute_called_matching "s3cret"
}

test_install_k3s_agent_reports_failure() {
  mock_fail sh
  run install_k3s_agent "https://m:6443"
  assert_output_contains "Failed to install k3s"
}

test_run_k3s_installer_never_evaluates_its_arguments() {
  run install_k3s_master "x; touch $TEST_TMP/pwned" ""
  assert_status 0
  [[ ! -e "$TEST_TMP/pwned" ]] || fail "argument was executed as shell code"
  assert_called "sh -s - server --server x; touch $TEST_TMP/pwned --flannel-backend wireguard-native"
}

test_show_k3s_token_prints_the_server_token() {
  mock_output sudo "K10abc::server:xyz"
  run show_k3s_token
  assert_status 0
  assert_calls "sudo cat /var/lib/rancher/k3s/server/token"
  assert_output_contains "K3S_TOKEN: "
  assert_output_contains "K10abc::server:xyz"
  assert_output_contains "Use it for joining agents and other master nodes to the cluster."
}

# ---------------------------------------------------- service control wrappers

# check_sudo_wrapper FUNCTION "SUDO ARGS" "ERROR MESSAGE"
check_sudo_wrapper() {
  run "$1"
  assert_status 0
  assert_calls "sudo $2"
  mock_fail sudo
  run "$1"
  assert_output_contains "$3"
}

test_start_k3s_master() { check_sudo_wrapper start_k3s_master "systemctl start k3s" "Failed to start k3s"; }
test_stop_k3s_master() { check_sudo_wrapper stop_k3s_master "systemctl stop k3s" "Failed to stop k3s"; }
test_start_k3s_agent() { check_sudo_wrapper start_k3s_agent "systemctl start k3s-agent" "Failed to start k3s agent"; }
test_stop_k3s_agent() { check_sudo_wrapper stop_k3s_agent "systemctl stop k3s-agent" "Failed to stop k3s agent"; }
test_killall_k3s() { check_sudo_wrapper killall_k3s "/usr/local/bin/k3s-killall.sh" "Failed to kill k3s"; }

test_uninstall_k3s_runs_uninstaller_when_confirmed() {
  script_fn question y
  run uninstall_k3s
  assert_status 0
  assert_called "sudo /usr/local/bin/k3s-uninstall.sh"
  assert_output_contains "Uninstalling k3s..."
}

# Regression: agents have no k3s-uninstall.sh, so uninstalling an agent failed
# with "command not found".
test_uninstall_k3s_runs_the_agent_script_on_agent_nodes() {
  : >"$K3S_SYSTEMD_DIR/k3s-agent.service"
  run uninstall_k3s --yes
  assert_status 0
  assert_calls "sudo /usr/local/bin/k3s-agent-uninstall.sh"
}

test_k3s_service_name_is_k3s_on_servers_and_k3s_agent_on_agents() {
  assert_eq "k3s" "$(k3s_service_name)"
  : >"$K3S_SYSTEMD_DIR/k3s-agent.service"
  assert_eq "k3s-agent" "$(k3s_service_name)"
}

test_uninstall_k3s_with_yes_does_not_ask() {
  script_fn question
  run uninstall_k3s --yes
  assert_status 0
  assert_called "sudo /usr/local/bin/k3s-uninstall.sh"
  assert_calls "sudo /usr/local/bin/k3s-uninstall.sh"
}

test_uninstall_k3s_does_nothing_when_declined() {
  script_fn question n
  run uninstall_k3s
  assert_status 0
  refute_called "sudo /usr/local/bin/k3s-uninstall.sh"
}

test_uninstall_k3s_reports_failure() {
  script_fn question y
  mock_fail sudo
  run uninstall_k3s
  assert_output_contains "Failed to uninstall k3s"
}

run_tests
