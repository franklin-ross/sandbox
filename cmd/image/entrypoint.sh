#!/bin/bash
set -euo pipefail

echo "Initializing sandbox firewall..."
sudo /opt/init-firewall.sh

mkdir -p /home/agent/.claude

# Match agent UID to the workspace directory owner so bind-mount writes work.
# After usermod, the current shell's UID is orphaned in /etc/passwd, so all
# sudo calls must happen in this block before the shell loses privilege.
if [ -n "${HOST_UID:-}" ] && [ "$HOST_UID" != "$(id -u)" ]; then
    sudo chown "$HOST_UID" /home/agent
    sudo chown -R "$HOST_UID" /home/agent/.claude
    sudo usermod -u "$HOST_UID" -o agent
fi

exec "$@"
