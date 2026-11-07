# shellcheck disable=SC2154
upgrade_barekube "${args[--yes]:+--yes}" "${args[--skip-signature-check]:+--skip-signature-check}"
