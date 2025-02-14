echo "# This file is located at './commands/install/dependencies.sh'."
echo "# It contains the implementation for the 'barekube install dependencies' command."
echo "# The code you write here will be wrapped by a function named 'barekube_install_dependencies_command()'."
echo "# Feel free to edit this file; your changes will persist when regenerating."
inspect_args
echo_info "Installing dependencies..."
update_packages
install_wireguard
echo_final "Failed to install dependencies." "Dependencies are installed succcessfully."
