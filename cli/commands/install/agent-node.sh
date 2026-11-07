# shellcheck disable=SC2154
check_k3s_installed "${args[--reinstall]}"
SERVER_URL=$(resolve_server_url "${args[--server]:-}" "${args[--yes]:+--yes}") || exit 1
K3S_TOKEN=$(resolve_token "${args[--yes]:+--yes}") || exit 1
export K3S_TOKEN
echo_info "Installing agent node..."

install_k3s_agent "$SERVER_URL"
setup_gvisor
echo_final "Failed to install k3s agent node." "Another k3s agent node installed successfully."
report_state "installed"
