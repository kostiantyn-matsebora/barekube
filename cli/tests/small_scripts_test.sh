#!/usr/bin/env bash
# Tests for src/longhorn.sh, src/system.sh and src/cli.sh.
source "$(dirname "${BASH_SOURCE[0]}")/lib/testing.sh"
source "$CLI_DIR/src/shared/colors.sh"
source "$CLI_DIR/src/shared/shared.sh"
source "$CLI_DIR/src/longhorn.sh"
source "$CLI_DIR/src/system.sh"
source "$CLI_DIR/src/cli.sh"

setup() {
  mock sudo barekube sha256sum cosign install check_if_command_exists
  mock_output barekube "0.2.0"
  HOME="$TEST_TMP/home"
  # wget creates the file it is asked to write, like the real one.
  wget() {
    echo "wget $*" >>"$CALLS"
    while (($#)); do
      [[ "$1" == "-O" ]] && : >"$2"
      shift
    done
    return 0
  }
  mock_wget_fail() { wget() { echo "wget $*" >>"$CALLS"; return 1; }; }
  # Work directories live under the test's own temp dir so leaks are visible.
  mktemp() { command mktemp -d "$TEST_TMP/work.XXXXXX"; }
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

RELEASE_URL="https://github.com/kostiantyn-matsebora/barekube/releases/latest/download"

test_upgrade_barekube_installs_a_verified_release_into_home_bin() {
  run upgrade_barekube --yes
  assert_status 0
  assert_called_matching "^wget -q $RELEASE_URL/barekube -O .*/barekube\$"
  assert_called_matching "^wget -q $RELEASE_URL/SHA256SUMS -O .*/SHA256SUMS\$"
  assert_called_matching "^wget -q $RELEASE_URL/barekube.sigstore.json -O .*/barekube.sigstore.json\$"
  assert_called "sha256sum --check --status SHA256SUMS"
  assert_called_matching "^cosign verify-blob --bundle .*/barekube.sigstore.json "
  assert_called_matching "^install -m 0755 .*/barekube $HOME/bin/barekube\$"
  assert_output_contains "Upgrading barekube CLI at $HOME/bin"
  assert_output_contains "upgraded successfully to version 0.2.0"
}

test_upgrade_barekube_verifies_before_installing() {
  run upgrade_barekube --yes
  assert_calls_in_order \
    "sha256sum --check --status SHA256SUMS" \
    "$(grep -m1 '^cosign ' "$CALLS")" \
    "$(grep -m1 '^install ' "$CALLS")"
}

test_upgrade_barekube_only_trusts_this_repositorys_release_workflow() {
  run upgrade_barekube --yes
  assert_called_matching "--certificate-identity-regexp \\^https://github\\\\.com/kostiantyn-matsebora/barekube/\\\\.github/workflows/release\\\\.yml@refs/tags/v"
  assert_called_matching "--certificate-oidc-issuer https://token.actions.githubusercontent.com "
}

test_upgrade_barekube_short_flag_is_automatic_too() {
  run upgrade_barekube -y
  assert_status 0
  assert_called_matching "^install -m 0755 .* $HOME/bin/barekube\$"
  refute_called_matching '^read_input'
}

test_upgrade_barekube_interactive_mode_defaults_to_home_bin() {
  script_fn read_input ""
  run upgrade_barekube
  assert_status 0
  assert_called "read_input Enter the path where workspace is installed ($HOME/bin as default): "
  assert_called_matching "^install -m 0755 .* $HOME/bin/barekube\$"
}

test_upgrade_barekube_interactive_mode_uses_the_entered_path() {
  script_fn read_input /opt/tools
  run upgrade_barekube
  assert_status 0
  assert_called_matching "^install -m 0755 .* /opt/tools/barekube\$"
}

test_upgrade_barekube_accepts_a_multi_character_path() {
  # Regression: the prompt once used `question`, which reads a single key.
  upgrade_from_stdin() { upgrade_barekube <<<"/opt/tools"; }
  run upgrade_from_stdin
  assert_called_matching "^install -m 0755 .* /opt/tools/barekube\$"
}

test_upgrade_barekube_leaves_the_installed_script_alone_when_the_download_fails() {
  mock_wget_fail
  run upgrade_barekube --yes
  assert_status 1
  assert_output_contains "Failed to download the release"
  assert_output_contains "Error upgrading barekube"
  refute_called_matching '^install '
  refute_output_contains "upgraded successfully"
}

test_upgrade_barekube_rejects_a_checksum_mismatch() {
  mock_fail sha256sum
  run upgrade_barekube --yes
  assert_status 1
  assert_output_contains "Checksum verification failed"
  refute_called_matching '^cosign '
  refute_called_matching '^install '
}

test_upgrade_barekube_rejects_a_bad_signature() {
  mock_fail cosign
  run upgrade_barekube --yes
  assert_status 1
  assert_output_contains "Signature verification failed"
  refute_called_matching '^install '
}

test_upgrade_barekube_refuses_without_cosign() {
  mock_fail check_if_command_exists cosign
  run upgrade_barekube --yes
  assert_status 1
  assert_output_contains "cosign is required to verify the release signature"
  refute_called_matching '^install '
}

test_upgrade_barekube_can_skip_the_signature_check_explicitly() {
  mock_fail check_if_command_exists cosign
  run upgrade_barekube --yes --skip-signature-check
  assert_status 0
  assert_output_contains "only the checksum was verified"
  refute_called_matching '^cosign '
  assert_called "sha256sum --check --status SHA256SUMS"
  assert_called_matching "^install -m 0755 "
}

test_upgrade_barekube_skip_flag_still_verifies_the_checksum() {
  mock_fail sha256sum
  run upgrade_barekube --yes --skip-signature-check
  assert_status 1
  refute_called_matching '^install '
}

test_upgrade_barekube_reports_an_install_failure() {
  mock_fail install
  run upgrade_barekube --yes
  assert_status 1
  assert_output_contains "Error upgrading barekube"
  refute_output_contains "upgraded successfully"
}

test_upgrade_barekube_always_removes_its_work_directory() {
  run upgrade_barekube --yes
  ! compgen -G "$TEST_TMP/work.*" >/dev/null || fail "work directory left behind"
  mock_fail sha256sum
  run upgrade_barekube --yes
  ! compgen -G "$TEST_TMP/work.*" >/dev/null || fail "work directory left behind after a failure"
}

run_tests
