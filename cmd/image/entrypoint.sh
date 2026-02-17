#!/bin/bash
set -euo pipefail

sudo /opt/init-firewall.sh

exec "$@"
