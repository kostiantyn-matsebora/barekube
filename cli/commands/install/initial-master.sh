echo "# This file is located at './commands/install/kubernetes/initial-master.sh'."
echo "# It contains the implementation for the 'barekube install kubernetes initial-master' command."
echo "# The code you write here will be wrapped by a function named 'barekube_install_kubernetes_initial_master_command()'."
echo "# Feel free to edit this file; your changes will persist when regenerating."
inspect_args
# shellcheck disable=SC2154
check_k3s_installed "${args[--reinstall]}"
echo_info "Installing initial k3s master node..."
ADDITIONAL_ARGS=$(gather_additional_args)


install_k3s_initial_master "${ADDITIONAL_ARGS}"
install_gvisor
register_gvisor_to_k3s
show_k3s_token
echo_final "Failed to install initial k3s master node." "Initial k3s master node installed successfully."
