#!/bin/bash
set -euo pipefail

# ============================================================
# Configurable firewall for sandbox containers
# ============================================================

# Flush existing rules
iptables -F OUTPUT
# Allow established connections (responses to allowed requests)
iptables -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
# Allow loopback
iptables -A OUTPUT -o lo -j ACCEPT
# Allow DNS (needed to resolve everything else)
iptables -A OUTPUT -p udp --dport 53 -j ACCEPT
iptables -A OUTPUT -p tcp --dport 53 -j ACCEPT

# Source generated rules if present
if [ -f /opt/ao-firewall-rules.sh ]; then
    source /opt/ao-firewall-rules.sh
fi

# Default deny everything else
iptables -A OUTPUT -j REJECT --reject-with icmp-port-unreachable

echo "Firewall initialized."
