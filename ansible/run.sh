#!/usr/bin/env bash
# Run Ansible for garmin_mcp inside a Docker container — the control node, since
# Ansible doesn't run natively on Windows. Requires Docker Desktop running.
#
# Usage (from Git Bash, in the ansible/ directory):
#   ./run.sh ansible all -m ping
#   ./run.sh ansible-playbook site.yml --ask-pass        # first (bootstrap) run
#   ./run.sh ansible-playbook site.yml                   # later runs (deploy + key)
#
# Your ~/.ssh is copied into the container and its key permissions fixed (mounting
# straight from Windows gives ssh world-readable keys, which it refuses). The
# Galaxy collections install once into ./collections (git-ignored).
set -euo pipefail

IMAGE="${ANSIBLE_IMAGE:-willhallonline/ansible:latest}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export MSYS_NO_PATHCONV=1  # stop Git Bash mangling container paths

if [ "$#" -eq 0 ]; then
  echo "usage: ./run.sh <ansible command...>   e.g. ./run.sh ansible-playbook site.yml --ask-pass" >&2
  exit 2
fi

# Allocate a TTY only when attached to one (needed for --ask-pass prompts);
# omit it for non-interactive callers (CI, the Claude Bash tool).
TTY_FLAGS="-i"
[ -t 0 ] && [ -t 1 ] && TTY_FLAGS="-it"

docker run --rm $TTY_FLAGS \
  --entrypoint /bin/sh \
  -e ANSIBLE_CONFIG=/ansible/ansible.cfg \
  -e ANSIBLE_COLLECTIONS_PATH=/ansible/collections \
  -e ANSIBLE_HOST_KEY_CHECKING=False \
  -e ANSIBLE_VAULT_PASSWORD_FILE=/root/.vault_pass \
  -v "${HERE}:/ansible" \
  -v "${HOME}/.ssh:/ssh-src:ro" \
  -w /ansible \
  "$IMAGE" \
  -c '
    set -e
    mkdir -p /root/.ssh
    cp -r /ssh-src/. /root/.ssh/ 2>/dev/null || true
    chmod 700 /root/.ssh
    chmod 600 /root/.ssh/* 2>/dev/null || true
    # Copy the vault password out of the world-writable Windows mount and strip
    # the exec bit, else ansible tries to run it as a password *script*.
    [ -f /ansible/.vault_pass ] && cp /ansible/.vault_pass /root/.vault_pass && chmod 600 /root/.vault_pass || true
    command -v sshpass >/dev/null 2>&1 || apk add --no-cache sshpass 2>/dev/null || true
    [ -d collections/ansible_collections ] || ansible-galaxy collection install -r requirements.yml -p collections
    exec "$@"
  ' garmin_mcp "$@"
