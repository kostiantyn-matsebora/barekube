# shellcheck disable=SC2154
GVISOR_DEFAULT_MODE=""
[[ -n "${args[--default]:-}" ]] && GVISOR_DEFAULT_MODE="on"
[[ -n "${args[--no-default]:-}" ]] && GVISOR_DEFAULT_MODE="off"
setup_gvisor "$GVISOR_DEFAULT_MODE"
