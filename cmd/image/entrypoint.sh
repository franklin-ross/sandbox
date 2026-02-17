#!/bin/bash
set -euo pipefail

echo "Initializing sandbox firewall..."
sudo /opt/init-firewall.sh

# Match agent UID to the workspace directory owner so bind-mount writes work.
if [ -n "${HOST_UID:-}" ] && [ "$HOST_UID" != "$(id -u)" ]; then
    sudo usermod -u "$HOST_UID" -o agent
    sudo chown -R agent:agent /home/agent
fi

mkdir -p /home/agent/.claude

exec "$@"
