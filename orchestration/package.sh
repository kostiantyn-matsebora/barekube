#!/usr/bin/env bash
# Packs the Ansible playbooks (tracked files only) into barekube-ansible.tar.gz,
# the archive published with every release.
#
#   orchestration/package.sh OUTPUT_DIR [REVISION]
set -euo pipefail
out="${1:?usage: package.sh OUTPUT_DIR [REVISION]}"
rev="${2:-HEAD}"
mkdir -p "$out"
git archive --format=tar.gz --prefix=barekube-ansible/ \
  --output "$out/barekube-ansible.tar.gz" "$rev:orchestration/ansible"
