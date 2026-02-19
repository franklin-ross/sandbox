#!/bin/bash
set -euo pipefail

# ============================================================
# Firewall for sandbox containers
#
# Rules are generated on the host (IPs pre-resolved) and applied
# atomically via iptables-restore / ip6tables-restore.
# ============================================================

if [ -f /opt/sandbox-firewall-rules.sh ]; then
    iptables-restore < /opt/sandbox-firewall-rules.sh
else
    # Basic lockdown until first sync pushes the rules file
    iptables -F OUTPUT
    iptables -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
    iptables -A OUTPUT -o lo -j ACCEPT
    iptables -A OUTPUT -p udp --dport 53 -j ACCEPT
    iptables -A OUTPUT -p tcp --dport 53 -j ACCEPT
    iptables -A OUTPUT -j REJECT --reject-with icmp-port-unreachable
fi

if [ -f /opt/sandbox-firewall-rules6.sh ]; then
    ip6tables-restore < /opt/sandbox-firewall-rules6.sh
else
    # Basic lockdown until first sync pushes the rules file
    ip6tables -F OUTPUT
    ip6tables -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
    ip6tables -A OUTPUT -o lo -j ACCEPT
    ip6tables -A OUTPUT -p udp --dport 53 -j ACCEPT
    ip6tables -A OUTPUT -p tcp --dport 53 -j ACCEPT
    ip6tables -A OUTPUT -j REJECT --reject-with icmp6-port-unreachable
fi

echo "Firewall initialized."
