#!/bin/bash
set -euo pipefail

echo "Initializing sandbox firewall..."
sudo /opt/init-firewall.sh

mkdir -p /home/agent/.claude

exec "$@"
