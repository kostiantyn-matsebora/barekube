echo_info "Installing longhorn dependencies..."
install_longhorn_dependencies

echo_final "Failed to install longhorn dependencies." "Longhorn dependencies are installed succcessfully."
echo_info "To fix multipath issue, please follow instructions: $(magenta """https://longhorn.io/kb/troubleshooting-volume-with-multipath/""")"

