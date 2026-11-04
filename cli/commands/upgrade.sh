echo "# This file is located at './commands/upgrade.sh'."
echo "# It contains the implementation for the 'barekube upgrade' command."
echo "# The code you write here will be wrapped by a function named 'barekube_upgrade_command()'."
echo "# Feel free to edit this file; your changes will persist when regenerating."
inspect_args
# shellcheck disable=SC2154
upgrade_barekube "${args[--yes]:+--yes}"
