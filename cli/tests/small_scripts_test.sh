#!/usr/bin/env bash
# Tests for src/haproxy.sh, src/longhorn.sh, src/system.sh and src/cli.sh.
source "$(dirname "${BASH_SOURCE[0]}")/lib/testing.sh"
source "$CLI_DIR/src/shared/colors.sh"
source "$CLI_DIR/src/shared/shared.sh"
source "$CLI_DIR/src/haproxy.sh"
source "$CLI_DIR/src/longhorn.sh"
source "$CLI_DIR/src/system.sh"
source "$CLI_DIR/src/cli.sh"

setup() {
  mock sudo wget chmod barekube
  mock_output barekube "0.2.0"
  HOME="$TEST_TMP/home"
}

# ------------------------------------------------------------------ haproxy

test_install_haproxy_installs_haproxy_and_keepalived() {
  run install_haproxy
  assert_status 0
  assert_calls "sudo apt-get install -y haproxy keepalived"
}

test_install_haproxy_reports_failure() {
  mock_fail sudo
  run install_haproxy
  assert_output_contains "Error installing HAProxy"
}

# ----------------------------------------------------------------- longhorn

test_install_longhorn_dependencies_installs_all_packages_in_order() {
  run install_longhorn_dependencies
  assert_status 0
  assert_calls \
    "sudo apt update" \
    "sudo apt install -y open-iscsi" \
    "sudo apt install -y bash" \
    "sudo apt install -y curl" \
    "sudo apt install -y grep" \
    "sudo apt install -y nfs-common"
}

test_install_longhorn_dependencies_reports_failure() {
  # Regression: the function once always ended with `return 0`, hiding failures.
  mock_fail sudo 'apt install -y open-iscsi'
  run install_longhorn_dependencies
  [[ "$status" -ne 0 ]] || fail "failed install reported as success"
}

# ------------------------------------------------------------------- system

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

test_enable_autoupdate_installs_configures_and_opens_the_config() {
  run enable_autoupdate
  assert_status 0
  assert_calls \
    "sudo apt update" \
    "sudo apt install -y unattended-upgrades" \
    "sudo dpkg-reconfigure --priority=low unattended-upgrades" \
    "sudo nano /etc/apt/apt.conf.d/50unattended-upgrades"
  assert_output_contains "Enabling automatic updates of system packages..."
  assert_output_contains "Automatic updates enabled."
}

# -------------------------------------------------------------------- upgrade

test_upgrade_barekube_automatic_mode_installs_into_home_bin() {
  run upgrade_barekube --yes
  assert_status 0
  assert_calls \
    "barekube --version" \
    "wget https://raw.githubusercontent.com/kostiantyn-matsebora/barekube/master/cli/release/barekube -O $HOME/bin/barekube" \
    "chmod +x $HOME/bin/barekube" \
    "barekube --version"
  assert_output_contains "Upgrading barekube CLI at $HOME/bin"
  assert_output_contains "upgraded successfully to version 0.2.0"
}

test_upgrade_barekube_short_flag_is_automatic_too() {
  run upgrade_barekube -y
  assert_status 0
  assert_called "chmod +x $HOME/bin/barekube"
  refute_called_matching '^read_input'
}

test_upgrade_barekube_interactive_mode_defaults_to_home_bin() {
  script_fn read_input ""
  run upgrade_barekube
  assert_status 0
  assert_called "read_input Enter the path where workspace is installed ($HOME/bin as default): "
  assert_called "chmod +x $HOME/bin/barekube"
}

test_upgrade_barekube_interactive_mode_uses_the_entered_path() {
  script_fn read_input /opt/tools
  run upgrade_barekube
  assert_status 0
  assert_called "wget https://raw.githubusercontent.com/kostiantyn-matsebora/barekube/master/cli/release/barekube -O /opt/tools/barekube"
  assert_called "chmod +x /opt/tools/barekube"
}

test_upgrade_barekube_reports_failure() {
  mock_fail chmod
  run upgrade_barekube --yes
  assert_output_contains "Error upgrading barekube"
  refute_output_contains "upgraded successfully"
}

test_upgrade_barekube_accepts_a_multi_character_path() {
  # Regression: the prompt once used `question`, which reads a single key.
  upgrade_from_stdin() { upgrade_barekube <<<"/opt/tools"; }
  run upgrade_from_stdin
  assert_called "chmod +x /opt/tools/barekube"
}

run_tests
