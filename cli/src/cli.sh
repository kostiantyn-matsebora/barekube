#!/bin/bash

BAREKUBE_RELEASE_URL="https://github.com/kostiantyn-matsebora/barekube/releases/latest/download"
# Releases are signed keylessly by the release workflow at a version tag.
BAREKUBE_SIGNER_IDENTITY='^https://github\.com/kostiantyn-matsebora/barekube/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$'
BAREKUBE_SIGNER_ISSUER="https://token.actions.githubusercontent.com"

# Prints the directory to install into: $HOME/bin when automatic, otherwise asked.
resolve_install_path() {
  local DEFAULT_PATH="$HOME/bin" CHOSEN_PATH
  if [[ "$(is_automatic_mode "$1")" == "0" ]]
  then
    echo "$DEFAULT_PATH"
    return 0
  fi
  CHOSEN_PATH="$(read_input "Enter the path where workspace is installed ($DEFAULT_PATH as default): ")"
  echo "${CHOSEN_PATH:-$DEFAULT_PATH}"
}

# Downloads the release script, its checksum and its signature bundle into DIR.
download_release() {
  local FILE
  for FILE in barekube SHA256SUMS barekube.sigstore.json
  do
    wget -q "$BAREKUBE_RELEASE_URL/$FILE" -O "$1/$FILE" || return 1
  done
}

verify_release_checksum() {
  echo_message "Verifying checksum..."
  (cd "$1" && sha256sum --check --status SHA256SUMS)
}

# Verifies the Sigstore signature, which proves the script was built by the
# release workflow of this repository. A checksum alone cannot: whoever can
# replace the script can replace the checksum next to it.
# Usage: verify_release_signature DIR [--skip-signature-check]
verify_release_signature() {
  if [[ "$2" == "--skip-signature-check" ]]
  then
    echo_info "Skipping signature verification; only the checksum was verified."
    return 0
  fi
  if ! check_if_command_exists cosign
  then
    echo_error "cosign is required to verify the release signature. Install it (https://docs.sigstore.dev/cosign/system_config/installation/) or pass --skip-signature-check."
    return 1
  fi
  echo_message "Verifying signature..."
  cosign verify-blob --bundle "$1/barekube.sigstore.json" \
    --certificate-identity-regexp "$BAREKUBE_SIGNER_IDENTITY" \
    --certificate-oidc-issuer "$BAREKUBE_SIGNER_ISSUER" "$1/barekube"
}

fetch_verified_release() {
  download_release "$1" || { echo_error "Failed to download the release"; return 1; }
  verify_release_checksum "$1" || { echo_error "Checksum verification failed"; return 1; }
  verify_release_signature "$1" "$2" || { echo_error "Signature verification failed"; return 1; }
}

# Installs the latest release only after it has been verified; the installed
# script is untouched when anything fails.
# Usage: upgrade_barekube [--yes|-y] [--skip-signature-check]
upgrade_barekube() {
  local BAREKUBE_PATH WORK_DIR STATUS
  echo_info "Upgrading barekube CLI $(barekube --version)"
  BAREKUBE_PATH="$(resolve_install_path "$1")"
  echo_message "Upgrading barekube CLI at $BAREKUBE_PATH"
  WORK_DIR="$(mktemp -d)" || { echo_error "Error upgrading barekube"; exit 1; }
  fetch_verified_release "$WORK_DIR" "$2" || { rm -rf "$WORK_DIR"; echo_error "Error upgrading barekube"; exit 1; }
  install -m 0755 "$WORK_DIR/barekube" "$BAREKUBE_PATH/barekube"
  STATUS=$?
  rm -rf "$WORK_DIR"
  if [[ "$STATUS" -ne 0 ]]
  then
    echo_error "Error upgrading barekube"
    exit "$STATUS"
  fi
  echo_info "Barekube CLI is upgraded successfully to version $(barekube --version)"
}
