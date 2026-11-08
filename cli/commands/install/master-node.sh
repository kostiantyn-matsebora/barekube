# shellcheck disable=SC2154
eval "TLS_SANS=(${args[--tls-san]:-})"
eval "DISABLE_COMPONENTS=(${args[--disable]:-})"
# On an installed node, changed server arguments are applied by running the
# installer again; with none given, or the same ones, there is nothing to do.
UPDATING=""
if [[ -z "${args[--reinstall]:-}" ]] && check_if_command_exists k3s \
    && { ((${#DISABLE_COMPONENTS[@]} + ${#TLS_SANS[@]})) || [[ -n "${args[--extra-args]:-}" ]]; }
then
    ADDITIONAL_ARGS=$(gather_additional_args "${args[--yes]:+--yes}" "${args[--extra-args]:-}")
    k3s_args_changed "$ADDITIONAL_ARGS" && UPDATING=1
fi
[[ -n "$UPDATING" ]] || check_k3s_installed "${args[--reinstall]}"
SERVER_URL=$(resolve_server_url "${args[--server]:-}" "${args[--yes]:+--yes}") || exit 1
K3S_TOKEN=$(resolve_token "${args[--yes]:+--yes}") || exit 1
export K3S_TOKEN
[[ -n "$UPDATING" ]] || ADDITIONAL_ARGS=$(gather_additional_args "${args[--yes]:+--yes}" "${args[--extra-args]:-}")
echo_info "Installing master node..."

echo_info "Joining cluster at: $SERVER_URL"
echo_info "Additional arguments: $ADDITIONAL_ARGS"

install_k3s_master "$SERVER_URL" "$ADDITIONAL_ARGS"
save_k3s_args "$ADDITIONAL_ARGS"
if [[ -n "$UPDATING" ]]
then
    echo_final "Failed to update k3s master node." "k3s master node updated successfully."
    report_state "updated"
    exit 0
fi
setup_gvisor "${args[--gvisor-default]:+on}"
echo_final "Failed to install k3s master node." "Another k3s master node installed successfully."
report_state "installed"
