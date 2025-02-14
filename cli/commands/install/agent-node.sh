echo "# This file is located at './commands/install/master.sh'."
echo "# It contains the implementation for the 'barekube install master' command."
echo "# The code you write here will be wrapped by a function named 'barekube_install_master_command()'."
echo "# Feel free to edit this file; your changes will persist when regenerating."
inspect_args
# shellcheck disable=SC2154
check_k3s_installed "${args[--reinstall]}"
MASTER_PARAMS=$(gather_master_params)
ADDITIONAL_ARGS=$(gather_additional_args)
echo_info "Installing agent node..."

install_k3s_agent "$MASTER_PARAMS" "$ADDITIONAL_ARGS"
install_gvisor
register_gvisor_to_k3s
echo_final "Failed to install k3s agent node." "Another k3s agent node installed successfully."
