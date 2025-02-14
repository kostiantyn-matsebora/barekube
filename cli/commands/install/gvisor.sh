echo "# This file is located at './commands/install/gvisor.sh'."
echo "# It contains the implementation for the 'barekube install gvisor' command."
echo "# The code you write here will be wrapped by a function named 'barekube_install_gvisor_command()'."
echo "# Feel free to edit this file; your changes will persist when regenerating."
inspect_args
install_gvisor
register_gvisor_to_k3s
