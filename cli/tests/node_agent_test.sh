#!/usr/bin/env bash
# Tests for src/node_agent.sh. The privileged commands run for real, but inside
# $TEST_TMP: sudo is a function that logs the call and runs the command, and the
# paths the code writes to point into the sandbox. Commands that would change
# the machine (systemctl, useradd, ...) are logging mocks.
source "$(dirname "${BASH_SOURCE[0]}")/lib/testing.sh"
source "$CLI_DIR/src/shared/colors.sh"
source "$CLI_DIR/src/shared/shared.sh"
source "$CLI_DIR/src/cli.sh"
source "$CLI_DIR/src/node_agent.sh"

setup() {
  SANDBOX="$TEST_TMP/root"
  mkdir -p "$SANDBOX"
  NODE_AGENT_BIN="$SANDBOX/barekube-node-agent"
  NODE_AGENT_CONF_DIR="$SANDBOX/conf"
  NODE_AGENT_UNIT="$SANDBOX/node-agent.service"
  NODE_AGENT_POLKIT_RULE="$SANDBOX/rules/49.rules"
  NODE_AGENT_SUDOERS="$SANDBOX/sudoers"
  NODE_AGENT_VIP_UNIT="$SANDBOX/barekube-vip-bootstrap.service"
  NODE_AGENT_VIP_HELPER="$SANDBOX/vip-helper"
  K3S_CONFIG_FILE="$SANDBOX/k3s-config.yaml"
  K3S_KUBECONFIG_FILE="$SANDBOX/k3s.yaml"
  NODE_AGENT_RELEASE_URL="https://example.test/v1"
  sudo() { printf '%s\n' "sudo $*" >>"$CALLS"; "$@"; }
  mock systemctl userdel apt-get chown visudo dpkg wget cosign
  # the account exists once useradd has been called
  id() { [[ -e "$SANDBOX/user" ]]; }
  useradd() { printf '%s\n' "useradd $*" >>"$CALLS"; touch "$SANDBOX/user"; }
  # install -o root needs root; copy instead
  install() { printf '%s\n' "install $*" >>"$CALLS"; cp "${@: -2:1}" "${@: -1}"; }
  sleep() { :; }
}

# ------------------------------------------------------------------ helpers

test_node_agent_release_url_defaults_to_this_version() {
  NODE_AGENT_RELEASE_URL=""
  barekube() { echo 1.2.3; }
  run node_agent_release_url
  assert_eq "https://github.com/kostiantyn-matsebora/barekube/releases/download/v1.2.3" "$output"
}

test_node_agent_release_url_can_be_overridden() {
  run node_agent_release_url
  assert_eq "https://example.test/v1" "$output"
}

test_node_agent_arch_maps_machine_names() {
  for pair in x86_64:amd64 aarch64:arm64 arm64:arm64 armv7l:arm; do
    uname() { echo "${pair%%:*}"; }
    run node_agent_arch
    assert_eq "${pair##*:}" "$output"
  done
}

test_node_agent_arch_rejects_unknown_machines() {
  uname() { echo riscv64; }
  run node_agent_arch
  assert_status 1
  assert_output_contains "Unsupported architecture: riscv64"
}

test_node_is_master_looks_for_the_server_kubeconfig() {
  run node_is_master
  assert_status 1
  touch "$K3S_KUBECONFIG_FILE"
  run node_is_master
  assert_status 0
}

test_resolve_console_token_uses_the_environment() {
  CONSOLE_NODE_TOKEN=abc run resolve_console_token
  assert_eq abc "$output"
}

test_resolve_console_token_requires_it_with_yes() {
  unset CONSOLE_NODE_TOKEN
  run resolve_console_token --yes
  assert_status 1
  assert_output_contains "CONSOLE_NODE_TOKEN is required with --yes."
}

test_resolve_console_token_prompts_otherwise() {
  unset CONSOLE_NODE_TOKEN
  script_fn read_required_input "from-prompt"
  run resolve_console_token ""
  assert_status 0
  assert_called "read_required_input Enter the console node token: Token is required."
}

test_validators() {
  valid_address 10.0.0.1 && valid_address node-1.lan
  ! valid_address 'a;b'
  ! valid_address ''
  valid_console_addr http://10.0.0.5:8080 && valid_console_addr https://console.lan
  ! valid_console_addr 'http://x:80/path'
  ! valid_console_addr 'ftp://x'
  valid_interval 5s && valid_interval 500ms && valid_interval 2m
  ! valid_interval 5 && ! valid_interval 'fast'
  valid_token abcdefghijklmnop0123 && valid_token 'a.b_c~d+e/f=g-hijklmn'
  ! valid_token short
  ! valid_token 'has space 0123456789'
  ! valid_token $'line\nbreak0123456789'
}

# ----------------------------------------------------------------- download

stage_release() {
  uname() { echo x86_64; }
  wget() {
    printf '%s\n' "wget $*" >>"$CALLS"
    local dest="$4"
    case "${2##*/}" in
      node-agent.SHA256SUMS) (cd "$(dirname "$dest")" && sha256sum node-agent-linux-amd64 >"$dest") ;;
      node-agent-linux-amd64) printf 'binary-content' >"$dest" ;;
      *) printf 'bundle' >"$dest" ;;
    esac
  }
}

fetch_release() {
  local work="$TEST_TMP/work"
  mkdir -p "$work"
  stage_release
  run fetch_node_agent "$work" "$@"
}

test_fetch_node_agent_downloads_verifies_and_prints_the_path() {
  mock_output cosign ""
  check_if_command_exists() { return 0; }
  fetch_release
  assert_status 0
  assert_called "wget -q https://example.test/v1/node-agent-linux-amd64 -O $TEST_TMP/work/node-agent-linux-amd64"
  assert_called "wget -q https://example.test/v1/node-agent.SHA256SUMS -O $TEST_TMP/work/node-agent.SHA256SUMS"
  assert_called "wget -q https://example.test/v1/node-agent-linux-amd64.sigstore.json -O $TEST_TMP/work/node-agent-linux-amd64.sigstore.json"
  assert_called_matching '^cosign verify-blob --bundle .*amd64.sigstore.json --certificate-identity-regexp .*release\\.yml'
  assert_output_line "$TEST_TMP/work/node-agent-linux-amd64"
}

test_fetch_node_agent_stops_on_a_bad_checksum() {
  check_if_command_exists() { return 0; }
  work="$TEST_TMP/work"; mkdir -p "$work"
  uname() { echo x86_64; }
  wget() {
    printf '%s\n' "wget $*" >>"$CALLS"
    case "${2##*/}" in
      node-agent.SHA256SUMS) echo "0000000000000000000000000000000000000000000000000000000000000000  node-agent-linux-amd64" >"$4" ;;
      *) echo tampered >"$4" ;;
    esac
  }
  run fetch_node_agent "$work" ""
  assert_status 1
  assert_output_contains "Checksum verification failed"
  refute_output_contains "cosign"
}

test_fetch_node_agent_stops_when_the_sums_do_not_list_the_file() {
  check_if_command_exists() { return 0; }
  wget() { echo "unrelated  other-file" >"$4"; }
  work="$TEST_TMP/work"; mkdir -p "$work"
  uname() { echo x86_64; }
  run fetch_node_agent "$work" ""
  assert_status 1
  assert_output_contains "Checksum verification failed"
}

test_fetch_node_agent_stops_when_a_download_fails() {
  uname() { echo x86_64; }
  wget() { return 1; }
  run fetch_node_agent "$TEST_TMP" ""
  assert_status 1
  assert_output_contains "Failed to download node-agent-linux-amd64"
}

test_fetch_node_agent_needs_cosign_unless_skipped() {
  check_if_command_exists() { return 1; }
  fetch_release
  assert_status 1
  assert_output_contains "cosign is required"
}

test_fetch_node_agent_can_skip_the_signature() {
  check_if_command_exists() { return 1; }
  fetch_release --skip-signature-check
  assert_status 0
  assert_output_contains "only the checksum was verified"
  ! _has_call_matching '^cosign'
}

test_fetch_node_agent_reports_a_bad_signature() {
  check_if_command_exists() { return 0; }
  mock_fail cosign
  fetch_release
  assert_status 1
  assert_output_contains "Signature verification failed"
}

test_fetch_node_agent_rejects_an_unknown_architecture() {
  uname() { echo riscv64; }
  run fetch_node_agent "$TEST_TMP" ""
  assert_status 1
}

# ------------------------------------------------------------------- pieces

test_install_node_agent_binary_installs_it_executable() {
  touch "$TEST_TMP/new"
  run install_node_agent_binary "$TEST_TMP/new"
  assert_status 0
  assert_called "sudo install -m 0755 -o root -g root $TEST_TMP/new $NODE_AGENT_BIN"
}

test_install_node_agent_binary_reports_failure() {
  sudo() { return 1; }
  run install_node_agent_binary /tmp/new
  assert_output_contains "Failed to install the node-agent binary"
}

test_create_node_agent_user_creates_a_system_account_that_cannot_log_in() {
  run create_node_agent_user
  assert_status 0
  assert_called "sudo useradd --system --no-create-home --shell /usr/sbin/nologin barekube-agent"
}

test_create_node_agent_user_keeps_an_existing_account() {
  id() { return 0; }
  run create_node_agent_user
  assert_output_contains "already exists"
  ! _has_call_matching '^sudo useradd'
}

test_certificate_is_generated_once() {
  openssl() { printf '%s\n' "openssl $*" >>"$CALLS"; touch "$NODE_AGENT_CONF_DIR/cert.pem" "$NODE_AGENT_CONF_DIR/key.pem"; }
  run create_node_agent_certificate
  assert_status 0
  assert_called_matching '^sudo openssl req -x509 -newkey rsa:2048 -nodes'
  assert_called "sudo chown -R barekube-agent:barekube-agent $NODE_AGENT_CONF_DIR"
  : >"$CALLS"
  run create_node_agent_certificate
  assert_output_contains "already exists"
  ! _has_call_matching 'openssl req'
}

test_certificate_failure_is_reported() {
  openssl() { return 1; }
  run create_node_agent_certificate
  assert_output_contains "Failed to generate the TLS certificate"
}

test_fingerprint_is_lower_case_without_colons() {
  openssl() { echo "sha256 Fingerprint=AB:CD:EF:01"; }
  run node_agent_fingerprint
  assert_eq abcdef01 "$output"
}

test_polkit_is_installed_when_missing() {
  dpkg() { return 1; }
  run install_polkit
  assert_called "sudo apt-get install -y polkitd"
}

test_polkit_is_not_reinstalled() {
  run install_polkit
  ! _has_call_matching 'apt-get'
  assert_called "sudo systemctl enable --now polkit"
}

test_polkit_install_failure_is_reported() {
  dpkg() { return 1; }
  mock_fail apt-get
  run install_polkit
  assert_output_contains "Failed to install polkit"
}

test_polkit_rules_of_an_agent_cannot_power_off() {
  run polkit_rules k3s-agent 0 0
  assert_output_contains 'action.lookup("unit") == "k3s-agent.service"'
  assert_output_contains 'login1.reboot'
  refute_output_contains 'power-off'
  refute_output_contains 'vip-bootstrap'
  assert_output_contains '});'
}

test_polkit_rules_of_a_master_can_power_off() {
  run polkit_rules k3s 1 0
  assert_output_contains 'action.lookup("unit") == "k3s.service"'
  assert_output_contains 'org.freedesktop.login1.power-off'
  refute_output_contains 'vip-bootstrap'
}

test_polkit_rules_name_the_vip_unit_only_with_a_vip() {
  run polkit_rules k3s 1 1
  assert_output_contains 'action.lookup("unit") == "barekube-vip-bootstrap.service"'
}

test_polkit_rules_never_grant_more_than_one_user() {
  run polkit_rules k3s 1 1
  assert_eq "$(grep -c 'subject.user == "barekube-agent"' <<<"$output")" "$(grep -c 'polkit.Result.YES' <<<"$output")"
}

test_write_managed_file_writes_and_marks_the_change() {
  NODE_AGENT_CHANGED=""
  write_managed_file "$SANDBOX/a/b" 0600 <<<"hello"
  assert_eq hello "$(<"$SANDBOX/a/b")"
  assert_eq 600 "$(stat -c %a "$SANDBOX/a/b")"
  assert_eq 1 "$NODE_AGENT_CHANGED"
}

test_write_managed_file_leaves_equal_content_alone() {
  mkdir -p "$SANDBOX/a"; echo hello >"$SANDBOX/a/b"
  NODE_AGENT_CHANGED=""
  write_managed_file "$SANDBOX/a/b" 0600 <<<"hello"
  assert_eq "" "$NODE_AGENT_CHANGED"
  ! _has_call_matching 'chmod'
}

test_write_managed_file_reports_a_write_failure() {
  sudo() { [[ "$1" == tee ]] && return 1; "$@"; }
  run write_managed_file "$SANDBOX/x" 0600 <<<"hello"
  assert_output_contains "Failed to write $SANDBOX/x"
}

test_install_polkit_rules_writes_the_rules_and_reloads() {
  run install_polkit_rules k3s 1 0
  assert_status 0
  assert_file_contains "$NODE_AGENT_POLKIT_RULE" 'k3s.service'
  assert_called "sudo systemctl reload polkit"
}

test_install_polkit_rules_restarts_polkit_when_it_cannot_reload() {
  mock_fail systemctl 'reload*'
  run install_polkit_rules k3s 1 0
  assert_called "sudo systemctl restart polkit"
}

test_vip_bootstrap_bakes_in_only_the_two_addresses() {
  run install_vip_bootstrap 10.0.0.9 10.0.0.1
  assert_status 0
  assert_file_contains "$NODE_AGENT_VIP_HELPER" "VIP=10.0.0.9"
  assert_file_contains "$NODE_AGENT_VIP_HELPER" "GATEWAY=10.0.0.1"
  [[ "$(<"$NODE_AGENT_VIP_HELPER")" != *@VIP@* ]] || fail "placeholder left"
  assert_file_contains "$NODE_AGENT_VIP_UNIT" "ExecStart=$NODE_AGENT_VIP_HELPER up"
  assert_file_contains "$NODE_AGENT_VIP_UNIT" "ExecStop=$NODE_AGENT_VIP_HELPER down"
  [[ "$(<"$NODE_AGENT_VIP_UNIT")" != *WantedBy* ]] || fail "the helper unit must never be enabled"
  sh -n "$NODE_AGENT_VIP_HELPER"
}

test_vip_helper_rejects_an_unknown_argument() {
  install_vip_bootstrap 10.0.0.9 10.0.0.1
  run sh "$NODE_AGENT_VIP_HELPER" bogus
  assert_status 2
}

test_remove_vip_bootstrap_deletes_both_files() {
  touch "$NODE_AGENT_VIP_UNIT" "$NODE_AGENT_VIP_HELPER"
  remove_vip_bootstrap
  [[ ! -e "$NODE_AGENT_VIP_UNIT" && ! -e "$NODE_AGENT_VIP_HELPER" ]] || fail "still there"
}

test_etcd_snapshot_rule_allows_that_one_command() {
  NODE_AGENT_CHANGED=""
  run install_etcd_snapshot_sudoers
  assert_status 0
  assert_file_contains "$NODE_AGENT_SUDOERS" "barekube-agent ALL=(root) NOPASSWD: /usr/local/bin/k3s etcd-snapshot save --name console-shutdown"
  assert_called_matching '^sudo visudo -cf '
}

test_etcd_snapshot_rule_is_not_rewritten() {
  install_etcd_snapshot_sudoers
  : >"$CALLS"
  run install_etcd_snapshot_sudoers
  ! _has_call_matching 'visudo'
}

test_invalid_sudoers_rule_is_not_installed() {
  mock_fail visudo
  run install_etcd_snapshot_sudoers
  assert_status 1
  assert_output_contains "sudoers rule is invalid"
  [[ ! -e "$NODE_AGENT_SUDOERS" && ! -e "$NODE_AGENT_SUDOERS.tmp" ]] || fail "left a rule behind"
}

test_token_file_is_private() {
  write_node_agent_token s3cret-token-value
  assert_file_contains "$NODE_AGENT_CONF_DIR/env" "NODE_AGENT_TOKEN=s3cret-token-value"
  assert_eq 600 "$(stat -c %a "$NODE_AGENT_CONF_DIR/env")"
}

test_unit_keeps_the_token_out_of_itself() {
  write_node_agent_unit k3s master-1 http://c:8080 5s
  assert_file_contains "$NODE_AGENT_UNIT" "User=barekube-agent"
  assert_file_contains "$NODE_AGENT_UNIT" "EnvironmentFile=$NODE_AGENT_CONF_DIR/env"
  assert_file_contains "$NODE_AGENT_UNIT" "-k3s-unit k3s -node-name master-1 -console-addr http://c:8080 -heartbeat-interval 5s"
  refute_contains "$(<"$NODE_AGENT_UNIT")" "TOKEN="
}

test_start_node_agent_restarts_so_changes_apply() {
  run start_node_agent
  assert_status 0
  assert_calls_in_order "sudo systemctl daemon-reload" "sudo systemctl restart barekube-node-agent.service"
}

test_start_node_agent_fails_when_it_does_not_run() {
  mock_fail systemctl 'is-active*'
  run start_node_agent
  assert_status 1
  assert_output_contains "The node-agent is not running"
}

# ---------------------------------------------------------------- the whole

stage_node() {
  printf 'binary-v1' >"$TEST_TMP/agent"
  openssl() {
    printf '%s\n' "openssl $*" >>"$CALLS"
    if [[ "$1" == req ]]; then mkdir -p "$NODE_AGENT_CONF_DIR"; touch "$NODE_AGENT_CONF_DIR/cert.pem" "$NODE_AGENT_CONF_DIR/key.pem"; fi
  }
}

run_setup() {
  setup_node_agent "$TEST_TMP/agent" tok-0123456789abcdef node-1 http://c:8080 5s "$@"
}

test_setup_on_an_agent_node() {
  stage_node
  run_setup "" ""
  assert_eq 1 "$NODE_AGENT_CHANGED"
  assert_file_contains "$NODE_AGENT_UNIT" "-k3s-unit k3s-agent"
  refute_contains "$(<"$NODE_AGENT_POLKIT_RULE")" "power-off"
  [[ ! -e "$NODE_AGENT_SUDOERS" && ! -e "$NODE_AGENT_VIP_UNIT" ]] || fail "master-only files on an agent"
  ! _has_call_matching 'restart k3s$'
  assert_called "sudo systemctl restart barekube-node-agent.service"
}

test_setup_on_a_master_without_a_vip() {
  stage_node
  touch "$K3S_KUBECONFIG_FILE"
  run_setup "" ""
  assert_file_contains "$NODE_AGENT_UNIT" "-k3s-unit k3s "
  assert_file_contains "$NODE_AGENT_POLKIT_RULE" "power-off"
  assert_file_contains "$NODE_AGENT_SUDOERS" "etcd-snapshot save"
  assert_file_contains "$K3S_CONFIG_FILE" "write-kubeconfig-group: barekube-agent"
  assert_called "sudo systemctl restart k3s"
  [[ ! -e "$NODE_AGENT_VIP_UNIT" ]] || fail "vip unit without a vip"
}

test_setup_on_a_master_with_a_vip() {
  stage_node
  touch "$K3S_KUBECONFIG_FILE"
  run_setup 10.0.0.9 10.0.0.1
  assert_file_contains "$NODE_AGENT_VIP_HELPER" "VIP=10.0.0.9"
  assert_file_contains "$NODE_AGENT_POLKIT_RULE" "barekube-vip-bootstrap.service"
}

test_setup_does_not_restart_k3s_when_the_kubeconfig_group_is_set() {
  stage_node
  touch "$K3S_KUBECONFIG_FILE"
  echo "write-kubeconfig-group: other" >"$K3S_CONFIG_FILE"
  run_setup "" ""
  ! _has_call_matching 'restart k3s$'
}

test_setup_twice_changes_nothing_the_second_time() {
  stage_node
  touch "$K3S_KUBECONFIG_FILE"
  run_setup 10.0.0.9 10.0.0.1
  : >"$CALLS"
  run_setup 10.0.0.9 10.0.0.1
  assert_eq "" "$NODE_AGENT_CHANGED"
  ! _has_call_matching 'restart barekube-node-agent'
}

test_setup_restarts_a_stopped_service_even_if_nothing_changed() {
  stage_node
  run_setup "" ""
  : >"$CALLS"
  mock_fail systemctl 'is-active --quiet barekube-node-agent.service'
  # is-active fails on the check, then also after the restart: only the call matters
  run run_setup "" ""
  assert_called "sudo systemctl restart barekube-node-agent.service"
}

test_setup_applies_a_new_binary_a_new_token_and_a_new_console() {
  stage_node
  run_setup "" ""
  for change in binary token console; do
    : >"$CALLS"
    case "$change" in
      binary) printf 'binary-v2' >"$TEST_TMP/agent"; run_setup "" "" ;;
      token) setup_node_agent "$TEST_TMP/agent" other-0123456789abcdef node-1 http://c:8080 5s "" "" ;;
      console) setup_node_agent "$TEST_TMP/agent" other-0123456789abcdef node-1 http://d:8080 5s "" "" ;;
    esac
    assert_called "sudo systemctl restart barekube-node-agent.service" "$change"
  done
}

test_setup_drops_the_vip_helper_when_the_vip_is_no_longer_given() {
  stage_node
  touch "$K3S_KUBECONFIG_FILE"
  run_setup 10.0.0.9 10.0.0.1
  run_setup "" ""
  [[ ! -e "$NODE_AGENT_VIP_UNIT" ]] || fail "stale vip unit"
}

test_remove_node_agent_removes_everything() {
  stage_node
  run_setup "" ""
  remove_node_agent
  for f in "$NODE_AGENT_UNIT" "$NODE_AGENT_BIN" "$NODE_AGENT_POLKIT_RULE" "$NODE_AGENT_CONF_DIR"; do
    [[ ! -e "$f" ]] || fail "$f remains"
  done
  assert_called "sudo systemctl disable --now barekube-node-agent.service"
  assert_called "sudo userdel barekube-agent"
}

# --------------------------------------------------------------- per node settings

test_valid_port() {
  valid_port 9091 && valid_port 65535 && valid_port 1
  ! valid_port 0 && ! valid_port 65536 && ! valid_port abc && ! valid_port ''
}

test_valid_url() {
  valid_url http://127.0.0.1:2381/metrics && valid_url https://etcd.lan
  ! valid_url 'http://x;rm'
  ! valid_url 'file:///etc'
}

test_valid_operations() {
  valid_operations reboot && valid_operations reboot,standby,rejoin,poweroff
  ! valid_operations ''
  ! valid_operations reboot,reboot
  ! valid_operations reboot,format
  ! valid_operations 'reboot,'
}

test_operations_default_to_everything_on_a_master_and_no_poweroff_on_an_agent() {
  node_agent_allows poweroff 1
  node_agent_allows reboot 0
  node_agent_allows standby 0
  ! node_agent_allows poweroff 0
}

test_operations_can_narrow_a_node() {
  NODE_AGENT_OPERATIONS="reboot,standby"
  node_agent_allows reboot 1 && node_agent_allows standby 1
  ! node_agent_allows poweroff 1
  ! node_agent_allows rejoin 1
}

test_an_agent_cannot_be_allowed_to_power_off() {
  NODE_AGENT_OPERATIONS="reboot,poweroff"
  ! node_agent_allows poweroff 0
}

test_polkit_rules_follow_the_operations() {
  NODE_AGENT_OPERATIONS="reboot"
  run polkit_rules k3s 1 0
  assert_output_contains 'login1.reboot'
  refute_output_contains 'manage-units'
  refute_output_contains 'power-off'
  NODE_AGENT_OPERATIONS="standby,rejoin"
  run polkit_rules k3s 1 0
  refute_output_contains 'login1'
  assert_output_contains 'manage-units'
}

test_a_node_without_poweroff_gets_no_snapshot_rule() {
  stage_node
  touch "$K3S_KUBECONFIG_FILE"
  NODE_AGENT_OPERATIONS="reboot,standby,rejoin"
  run_setup "" ""
  [[ ! -e "$NODE_AGENT_SUDOERS" ]] || fail "snapshot rule without poweroff"
}

test_unit_uses_the_port_and_the_etcd_metrics_url() {
  NODE_AGENT_PORT=9199
  NODE_AGENT_ETCD_METRICS=http://127.0.0.1:2399/metrics
  write_node_agent_unit k3s master-1 http://c:8080 5s
  assert_file_contains "$NODE_AGENT_UNIT" "-addr :9199 "
  assert_file_contains "$NODE_AGENT_UNIT" "-heartbeat-interval 5s -etcd-metrics-url http://127.0.0.1:2399/metrics"
}

test_unit_has_no_etcd_argument_by_default() {
  write_node_agent_unit k3s master-1 http://c:8080 5s
  refute_contains "$(<"$NODE_AGENT_UNIT")" "etcd-metrics"
}

run_tests
