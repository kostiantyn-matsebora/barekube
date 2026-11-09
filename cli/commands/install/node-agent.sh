# shellcheck disable=SC2154
if [[ -n "${args[--remove]:-}" ]]
then
    echo_info "Removing the node-agent..."
    remove_node_agent
    echo_final "Failed to remove the node-agent." "The node-agent is removed."
    report_state "removed"
    exit 0
fi

CONSOLE_ADDR="${args[--console-addr]:-}"
NODE_NAME="${args[--node-name]:-$(hostname)}"
INTERVAL="${args[--heartbeat-interval]}"
API_VIP="${args[--api-vip]:-}"
API_GATEWAY="${args[--api-gateway]:-}"
NODE_AGENT_PORT="${args[--port]:-$NODE_AGENT_PORT}"
NODE_AGENT_OPERATIONS="${args[--operations]:-}"
NODE_AGENT_ETCD_METRICS="${args[--etcd-metrics-url]:-}"
[[ -n "$CONSOLE_ADDR" ]] || { echo_error "--console-addr is required"; exit 1; }
valid_console_addr "$CONSOLE_ADDR" || { echo_error "--console-addr must look like http://host:8080"; exit 1; }
valid_address "$NODE_NAME" || { echo_error "--node-name may only have letters, digits, dots and dashes"; exit 1; }
valid_interval "$INTERVAL" || { echo_error "--heartbeat-interval must be a duration such as 5s"; exit 1; }
valid_port "$NODE_AGENT_PORT" || { echo_error "--port must be a number from 1 to 65535"; exit 1; }
[[ -z "$NODE_AGENT_OPERATIONS" ]] || valid_operations "$NODE_AGENT_OPERATIONS" \
    || { echo_error "--operations must list some of reboot, standby, rejoin and poweroff, separated by commas"; exit 1; }
[[ -z "$NODE_AGENT_ETCD_METRICS" ]] || valid_url "$NODE_AGENT_ETCD_METRICS" \
    || { echo_error "--etcd-metrics-url must be an http or https URL"; exit 1; }
if [[ -n "$API_VIP$API_GATEWAY" ]]
then
    [[ -n "$API_VIP" && -n "$API_GATEWAY" ]] && valid_address "$API_VIP" && valid_address "$API_GATEWAY" \
        || { echo_error "--api-vip and --api-gateway must be given together, as plain addresses"; exit 1; }
fi

CONSOLE_NODE_TOKEN=$(resolve_console_token "${args[--yes]:+--yes}") || exit 1
valid_token "$CONSOLE_NODE_TOKEN" || { echo_error "The token needs at least 16 characters from A-Z a-z 0-9 . _ ~ + / = -"; exit 1; }

echo_info "Installing the node-agent..."
if [[ -n "${args[--binary]:-}" ]]
then
    NODE_AGENT_FILE="${args[--binary]}"
    [[ -f "$NODE_AGENT_FILE" ]] || { echo_error "No such file: $NODE_AGENT_FILE"; exit 1; }
else
    WORK_DIR="$(mktemp -d)" || exit 1
    NODE_AGENT_FILE="$(fetch_node_agent "$WORK_DIR" "${args[--skip-signature-check]:+--skip-signature-check}")" \
        || { rm -rf "$WORK_DIR"; exit 1; }
fi
setup_node_agent "$NODE_AGENT_FILE" "$CONSOLE_NODE_TOKEN" "$NODE_NAME" "$CONSOLE_ADDR" "$INTERVAL" "$API_VIP" "$API_GATEWAY"
[[ -z "${WORK_DIR:-}" ]] || rm -rf "$WORK_DIR"
echo_final "Failed to install the node-agent." "The node-agent runs on $NODE_NAME."
# The fingerprint is for the console to pin; it is not a secret.
echo "fingerprint $(node_agent_fingerprint)"
if [[ -n "$NODE_AGENT_CHANGED" ]]
then
    report_state "installed"
else
    report_state "already installed"
fi
