#!/usr/bin/env bash
# Tests for src/gvisor.sh. External commands are replaced by logging mocks.
source "$(dirname "${BASH_SOURCE[0]}")/lib/testing.sh"
source "$CLI_DIR/src/shared/colors.sh"
source "$CLI_DIR/src/shared/shared.sh"
source "$CLI_DIR/src/gvisor.sh"

CONFIG_DIR=/var/lib/rancher/k3s/agent/etc/containerd
KEYRING=/usr/share/keyrings/gvisor-archive-keyring.gpg

setup() {
  mock sudo curl dpkg
  K3S_SYSTEMD_DIR="$TEST_TMP/systemd"
  mkdir -p "$K3S_SYSTEMD_DIR"
  mock_capture_stdin sudo
  mock_output dpkg amd64
}

# --------------------------------------------------------------- installation

test_install_gvisor_prerequisites_installs_apt_helpers() {
  run install_gvisor_prerequisites
  assert_status 0
  assert_calls "sudo apt install -y apt-transport-https ca-certificates curl gnupg"
}

test_install_gvisor_prerequisites_reports_failure() {
  mock_fail sudo
  run install_gvisor_prerequisites
  assert_output_contains "Failed to install gVisor dependencies"
}

test_add_gvisor_apt_key_downloads_and_dearmors_the_key() {
  run add_gvisor_apt_key
  assert_status 0
  # the two sides of the pipe run concurrently, so only membership is asserted
  assert_called "curl -fsSL https://gvisor.dev/archive.key"
  assert_called "sudo gpg --dearmor -o $KEYRING"
}

test_add_gvisor_apt_key_reports_failure() {
  mock_fail sudo 'gpg*'
  run add_gvisor_apt_key
  assert_output_contains "Failed to add gVisor key"
}

test_add_gvisor_apt_repository_writes_source_for_the_host_architecture() {
  run add_gvisor_apt_repository
  assert_status 0
  assert_called "sudo tee /etc/apt/sources.list.d/gvisor.list"
  assert_file_contains "$STDIN_LOG" \
    "deb [arch=amd64 signed-by=$KEYRING] https://storage.googleapis.com/gvisor/releases release main"
}

test_add_gvisor_apt_repository_reports_failure() {
  mock_fail sudo 'tee*'
  run add_gvisor_apt_repository
  assert_output_contains "Failed to add gVisor repository"
}

test_install_gvisor_package_updates_apt_then_installs_runsc() {
  run install_gvisor_package
  assert_status 0
  assert_calls "sudo apt-get update" "sudo apt-get install -y runsc"
}

test_install_gvisor_package_reports_install_failure() {
  mock_fail sudo 'apt-get install*'
  run install_gvisor_package
  assert_output_contains "Failed to install gVisor"
}

test_install_gvisor_package_skips_install_when_update_fails() {
  mock_fail sudo 'apt-get update'
  run install_gvisor_package
  assert_output_contains "Failed to install gVisor"
  refute_called "sudo apt-get install -y runsc"
}

test_install_gvisor_runs_every_step() {
  run install_gvisor
  assert_status 0
  assert_called "sudo apt install -y apt-transport-https ca-certificates curl gnupg"
  assert_called "sudo gpg --dearmor -o $KEYRING"
  assert_called "sudo tee /etc/apt/sources.list.d/gvisor.list"
  assert_called "sudo apt-get install -y runsc"
}

test_install_gvisor_stops_at_the_first_failing_step() {
  mock_fail sudo 'tee*'
  run install_gvisor
  assert_output_contains "Failed to add gVisor repository"
  refute_called "sudo apt-get install -y runsc"
}

# --------------------------------------------------------------- registration

test_runsc_runtime_config_declares_the_runtime() {
  run runsc_runtime_config
  assert_status 0
  assert_output_contains '[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]'
  assert_output_contains 'runtime_type = "io.containerd.runsc.v1"'
  assert_output_contains 'ConfigPath = "/etc/containerd/runsc.toml"'
}

test_register_gvisor_does_nothing_when_runtime_is_already_configured() {
  run register_gvisor_to_k3s
  assert_status 0
  assert_calls "sudo grep -qs .containerd.runtimes.runsc $CONFIG_DIR/config.toml.tmpl"
}

test_register_gvisor_adds_runtime_to_template_and_restarts_k3s() {
  mock_fail sudo 'grep*'
  run register_gvisor_to_k3s
  assert_status 0
  assert_called "sudo cp $CONFIG_DIR/config.toml $CONFIG_DIR/config.toml.tmpl"
  assert_called "sudo tee -a $CONFIG_DIR/config.toml.tmpl"
  assert_called "sudo systemctl restart k3s"
  assert_file_contains "$STDIN_LOG" '[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]'
  assert_file_contains "$STDIN_LOG" 'runtime_type = "io.containerd.runsc.v1"'
}

# Regression: an agent node has no k3s.service, so restarting it failed the
# whole agent install ("Unit k3s.service not found").
test_register_gvisor_restarts_the_agent_service_on_agent_nodes() {
  : >"$K3S_SYSTEMD_DIR/k3s-agent.service"
  mock_fail sudo 'grep*'
  run register_gvisor_to_k3s
  assert_status 0
  assert_called "sudo systemctl restart k3s-agent"
  refute_called "sudo systemctl restart k3s"
  assert_output_contains "Restarting k3s-agent..."
}

test_k3s_service_name_is_k3s_on_servers_and_k3s_agent_on_agents() {
  assert_eq "k3s" "$(k3s_service_name)"
  : >"$K3S_SYSTEMD_DIR/k3s-agent.service"
  assert_eq "k3s-agent" "$(k3s_service_name)"
}

# Regression: the template was copied without sudo, which fails for anyone who
# is not root because the containerd directory belongs to root.
test_register_gvisor_copies_the_template_with_sudo() {
  mock_fail sudo 'grep*'
  run register_gvisor_to_k3s
  refute_called "cp $CONFIG_DIR/config.toml $CONFIG_DIR/config.toml.tmpl"
  assert_called "sudo cp $CONFIG_DIR/config.toml $CONFIG_DIR/config.toml.tmpl"
}

test_register_gvisor_stops_when_the_template_cannot_be_copied() {
  mock_fail sudo 'grep*'
  # grep and cp both fail for this mock; only the copy failure must be reported
  sudo() { echo "sudo $*" >>"$CALLS"; case "$1" in grep) return 1 ;; cp) return 1 ;; esac; }
  run register_gvisor_to_k3s
  assert_status 1
  assert_output_contains "Failed to copy the containerd configuration"
  refute_called_matching '^sudo tee'
}

test_register_gvisor_reports_template_write_failure() {
  mock_fail sudo 'grep*'
  sudo() { echo "sudo $*" >>"$CALLS"; case "$1" in grep) return 1 ;; tee) return 1 ;; esac; }
  run register_gvisor_to_k3s
  assert_output_contains "Failed to add runsc runtime to containerd configuration"
  refute_called "sudo systemctl restart k3s"
}

test_register_gvisor_reports_restart_failure() {
  sudo() { echo "sudo $*" >>"$CALLS"; case "$1" in grep) return 1 ;; systemctl) return 1 ;; esac; }
  run register_gvisor_to_k3s
  assert_output_contains "Failed to register gVisor to k3s"
}

test_setup_gvisor_installs_then_registers() {
  mock_fail sudo 'grep*'
  run setup_gvisor
  assert_status 0
  assert_called "sudo apt-get install -y runsc"
  assert_called "sudo systemctl restart k3s"
}

run_tests
