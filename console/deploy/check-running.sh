#!/usr/bin/env bash
# Checks a running console from the outside: the three doors open only for their
# own secret, and the UI answers once signed in. Changes nothing in the cluster.
#
#   check-running.sh URL UI_TOKEN NODE_TOKEN [NODE_NAME]
#
# With NODE_NAME (a node of the console's config) it also sends one heartbeat.
set -euo pipefail
url="${1:?usage: $0 URL UI_TOKEN NODE_TOKEN [NODE_NAME]}"
ui="${2:?}" node_token="${3:?}" node="${4:-}"
jar="$(mktemp)"
trap 'rm -f "$jar"' EXIT

code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }
expect() {
  local want="$1" what="$2"
  shift 2
  local got
  got="$(code "$@")"
  if [[ "$got" != "$want" ]]; then
    echo "FAIL: $what: expected HTTP $want, got $got" >&2
    exit 1
  fi
  echo "ok: $what ($got)"
}

for _ in $(seq 1 30); do
  [[ "$(code "$url/login" || true)" == 200 ]] && break
  sleep 1
done

expect 200 "the sign-in page is open" "$url/login"
expect 303 "the UI sends an anonymous visitor to sign in" "$url/"
expect 401 "the API refuses an anonymous visitor" "$url/api/nodes"
expect 401 "a wrong UI token is refused" -d token=wrong "$url/login"
expect 401 "the node token does not sign in" -d "token=$node_token" "$url/login"
expect 303 "the UI token signs in" -c "$jar" -d "token=$ui" "$url/login"
expect 200 "the API answers a signed-in session" -b "$jar" "$url/api/nodes"
expect 401 "a node action needs a session or the automation token" -X POST "$url/api/nodes/none/reboot"
expect 401 "a heartbeat without a token is refused" -X POST "$url/api/internal/heartbeat"
expect 401 "the UI token is not a node token" -X POST -H "Authorization: Bearer $ui" "$url/api/internal/heartbeat"
if [[ -n "$node" ]]; then
  expect 200 "a heartbeat with the node token is accepted" -X POST -H "Authorization: Bearer $node_token" \
    -H 'Content-Type: application/json' \
    -d "{\"node\":\"$node\",\"timestamp\":\"$(date -u +%FT%TZ)\",\"k3sActive\":true}" "$url/api/internal/heartbeat"
fi
echo "console at $url: all checks passed"
