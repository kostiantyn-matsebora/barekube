echo "# This file is located at './commands/longhorn/install-dependencies.sh'."
echo "# It contains the implementation for the 'barekube longhorn install-dependencies' command."
echo "# The code you write here will be wrapped by a function named 'barekube_longhorn_install_dependencies_command()'."
echo "# Feel free to edit this file; your changes will persist when regenerating."
inspect_args

echo_info "Installing longhorn dependencies..."
install_longhorn_dependencies

echo_final "Failed to install longhorn dependencies." "Longhorn dependencies are installed succcessfully."
echo_info "To fix multipath issue, please follow instructions: $(magenta """https://longhorn.io/kb/troubleshooting-volume-with-multipath/""")"

