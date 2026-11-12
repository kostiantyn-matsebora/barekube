#!/usr/bin/env bash
# Prints what is needed to understand a failed run. Never fails.
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

group "Nodes and pods"
kc get nodes -o wide || true
kc get pods -A -o wide || true
kc get events -A --sort-by=.lastTimestamp | tail -40 || true
endgroup

for n in "${NODES[@]}"; do
  group "Service logs on $n"
  node "$n" systemctl --no-pager status k3s k3s-agent 2>&1 | head -30 || true
  node "$n" journalctl -u k3s -u k3s-agent --no-pager -n 120 2>&1 || true
  node "$n" wg show 2>&1 | head -20 || true
  endgroup
done

if console_enabled; then
  group "Console"
  tail -n 80 "$STATE_DIR/logs/console.log" 2>/dev/null || true
  cat "$CONSOLE_DIR/config.yaml" 2>/dev/null || true
  curl -fsS "http://127.0.0.1:$CONSOLE_PORT/login" -o /dev/null && echo "the console answers" || echo "the console does not answer"
  for n in "${NODES[@]}"; do
    echo "--- node-agent on $n"
    node "$n" systemctl --no-pager status barekube-node-agent 2>&1 | head -15 || true
    node "$n" journalctl -u barekube-node-agent --no-pager -n 60 2>&1 || true
  done
  endgroup
fi

group "Provisioning logs"
tail -n 40 "$STATE_DIR"/logs/*.log 2>/dev/null || true
endgroup
exit 0
