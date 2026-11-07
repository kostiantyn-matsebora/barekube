# shellcheck disable=SC2154
check_k3s_installed "${args[--reinstall]}"
eval "TLS_SANS=(${args[--tls-san]:-})"
eval "DISABLE_COMPONENTS=(${args[--disable]:-})"
echo_info "Installing initial k3s master node..."
ADDITIONAL_ARGS=$(gather_additional_args "${args[--yes]:+--yes}" "${args[--extra-args]:-}")

install_k3s_initial_master "${ADDITIONAL_ARGS}"
setup_gvisor
show_k3s_token
echo_final "Failed to install initial k3s master node." "Initial k3s master node installed successfully."
report_state "installed"
