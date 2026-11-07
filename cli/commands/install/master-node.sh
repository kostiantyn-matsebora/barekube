# shellcheck disable=SC2154
check_k3s_installed "${args[--reinstall]}"
SERVER_URL=$(resolve_server_url "${args[--server]:-}" "${args[--yes]:+--yes}") || exit 1
K3S_TOKEN=$(resolve_token "${args[--yes]:+--yes}") || exit 1
export K3S_TOKEN
eval "TLS_SANS=(${args[--tls-san]:-})"
eval "DISABLE_COMPONENTS=(${args[--disable]:-})"
ADDITIONAL_ARGS=$(gather_additional_args "${args[--yes]:+--yes}" "${args[--extra-args]:-}")
echo_info "Installing master node..."

echo_info "Joining cluster at: $SERVER_URL"
echo_info "Additional arguments: $ADDITIONAL_ARGS"

install_k3s_master "$SERVER_URL" "$ADDITIONAL_ARGS"
setup_gvisor "${args[--gvisor-default]:+on}"
echo_final "Failed to install k3s master node." "Another k3s master node installed successfully."
report_state "installed"
