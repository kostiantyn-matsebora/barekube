#!/usr/bin/env bash
# Tests for src/k3s.sh. External commands are replaced by logging mocks.
source "$(dirname "${BASH_SOURCE[0]}")/lib/testing.sh"
source "$CLI_DIR/src/shared/shared.sh"
source "$CLI_DIR/src/k3s.sh"

CONFIG_DIR=/var/lib/rancher/k3s/agent/etc/containerd

setup() {
  mock sudo curl sh cp grep dpkg systemctl check_if_command_exists
  mock_capture_stdin sudo
  mock_output dpkg amd64
}

# --------------------------------------------------------------- packages

test_update_packages_runs_apt_update() {
  run update_packages
  assert_status 0
  assert_calls "sudo apt update"
  assert_output_contains "Updating packages..."
}

test_update_packages_reports_failure() {
  mock_fail sudo
  run update_packages
  assert_output_contains "Failed to update packages"
}

test_install_containerd_installs_the_package() {
  run install_containerd
  assert_status 0
  assert_calls "sudo apt install -y containerd"
}

test_install_containerd_reports_failure() {
  mock_fail sudo
  run install_containerd
  assert_output_contains "Failed to install containerd"
}

test_install_wireguard_installs_the_package() {
  run install_wireguard
  assert_status 0
  assert_calls "sudo apt install -y wireguard"
}

test_install_wireguard_reports_failure() {
  mock_fail sudo
  run install_wireguard
  assert_output_contains "Failed to install wireguard"
}

# ----------------------------------------------------------------- gVisor

test_install_gvisor_installs_prerequisites_key_repository_and_runsc() {
  run install_gvisor
  assert_status 0
  # curl|gpg|tee run concurrently, so only membership is asserted here.
  assert_called "sudo apt install -y apt-transport-https ca-certificates curl gnupg"
  assert_called "curl -fsSL https://gvisor.dev/archive.key"
  assert_called "sudo gpg --dearmor -o /usr/share/keyrings/gvisor-archive-keyring.gpg"
  assert_called "sudo tee /etc/apt/sources.list.d/gvisor.list"
  assert_called "sudo apt-get update"
  assert_called "sudo apt-get install -y runsc"
}

test_install_gvisor_writes_apt_source_for_the_host_architecture() {
  run install_gvisor
  assert_file_contains "$STDIN_LOG" \
    "deb [arch=amd64 signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] https://storage.googleapis.com/gvisor/releases release main"
}

test_install_gvisor_reports_repository_failure() {
  mock_fail sudo 'tee*'
  run install_gvisor
  assert_output_contains "Failed to add gVisor key"
  refute_called "sudo apt-get install -y runsc"
}

test_install_gvisor_reports_package_failure() {
  mock_fail sudo 'apt-get install*'
  run install_gvisor
  assert_output_contains "Failed to install gVisor"
}

test_install_gvisor_skips_runsc_when_apt_update_fails() {
  mock_fail sudo 'apt-get update'
  run install_gvisor
  assert_output_contains "Failed to install gVisor"
  refute_called "sudo apt-get install -y runsc"
}

test_register_gvisor_does_nothing_when_runtime_is_already_configured() {
  run register_gvisor_to_k3s
  assert_status 0
  assert_calls "grep -q .containerd.runtimes.runsc $CONFIG_DIR/config.toml.tmpl"
}

test_register_gvisor_adds_runtime_to_template_and_restarts_k3s() {
  mock_fail grep
  run register_gvisor_to_k3s
  assert_status 0
  assert_called "cp $CONFIG_DIR/config.toml $CONFIG_DIR/config.toml.tmpl"
  assert_called "sudo tee -a $CONFIG_DIR/config.toml.tmpl"
  assert_called "sudo systemctl restart k3s"
  assert_file_contains "$STDIN_LOG" '[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]'
  assert_file_contains "$STDIN_LOG" 'runtime_type = "io.containerd.runsc.v1"'
  assert_file_contains "$STDIN_LOG" 'ConfigPath = "/etc/containerd/runsc.toml"'
}

test_register_gvisor_reports_template_write_failure() {
  mock_fail grep
  mock_fail sudo 'tee*'
  run register_gvisor_to_k3s
  assert_output_contains "Failed to add runsc runtime to containerd configuration"
  refute_called "sudo systemctl restart k3s"
}

test_register_gvisor_reports_restart_failure() {
  mock_fail grep
  mock_fail sudo 'systemctl*'
  run register_gvisor_to_k3s
  assert_output_contains "Failed to register gVisor to k3s"
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
  refute_output_contains "continued"
}

test_check_k3s_installed_continues_when_reinstall_requested() {
  check_k3s_wrapper() { check_k3s_installed "$1"; echo "continued"; }
  run check_k3s_wrapper 1
  assert_status 0
  assert_output_contains "k3s is already installed."
  assert_output_contains "continued"
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

test_gather_master_params_builds_server_and_token_flags() {
  script_fn read_input https://10.0.0.1:6443 s3cret
  run gather_master_params
  assert_status 0
  assert_eq " --server=https://10.0.0.1:6443 --token=s3cret" "$output"
}

test_gather_master_params_requires_a_master_url() {
  script_fn read_input ""
  run gather_master_params
  assert_status 1
  assert_output_contains "Master URL is required."
}

test_gather_master_params_requires_a_token() {
  script_fn read_input https://10.0.0.1:6443 ""
  run gather_master_params
  assert_status 1
  assert_output_contains "Token is required."
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

known_bug_install_k3s_initial_master_forwards_additional_arguments() {
  # The function reads its extra arguments from $2, but the command passes
  # them as $1, so --disable/--tls-san choices are silently dropped.
  run install_k3s_initial_master "--disable=traefik"
  assert_called "sh -s - server --cluster-init --flannel-backend wireguard-native --disable=traefik"
}

test_install_k3s_master_joins_with_params_and_additional_arguments() {
  run install_k3s_master "--server=https://m:6443 --token=t" "--disable=traefik"
  assert_status 0
  assert_called "sh -s - server --server=https://m:6443 --token=t --flannel-backend wireguard-native --disable=traefik"
}

test_install_k3s_master_reports_failure() {
  mock_fail sh
  run install_k3s_master "--server=x --token=t" ""
  assert_output_contains "Failed to install k3s"
}

test_install_k3s_agent_joins_with_params() {
  run install_k3s_agent "--server=https://m:6443 --token=t"
  assert_status 0
  assert_called "curl -sfL https://get.k3s.io"
  assert_called "sh -s - agent --server=https://m:6443 --token=t"
}

test_install_k3s_agent_reports_failure() {
  mock_fail sh
  run install_k3s_agent "--server=x --token=t"
  assert_output_contains "Failed to install k3s"
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
