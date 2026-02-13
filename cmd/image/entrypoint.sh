#!/bin/bash
set -euo pipefail

echo "Initializing sandbox firewall..."
sudo /opt/init-firewall.sh

mkdir -p /home/agent/.claude

# Generate env file for docker exec --env-file to pick up.
env_file=/home/agent/.sandbox-env
: > "$env_file"

if [ -f /home/agent/.claude/.anthropic-key ]; then
  echo "ANTHROPIC_API_KEY=$(cat /home/agent/.claude/.anthropic-key)" >> "$env_file"
  export ANTHROPIC_API_KEY=$(cat /home/agent/.claude/.anthropic-key)
fi

exec "$@"
