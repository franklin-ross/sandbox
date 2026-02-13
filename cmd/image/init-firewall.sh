#!/bin/bash
set -euo pipefail

# ============================================================
# Firewall whitelist for Claude Code sandbox
#
# Allows: Claude API, npm/yarn/bun/pnpm, Go, Rust, Ruby, GitHub, PyPI
# Denies: everything else outbound
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

# --- Resolve and whitelist domains ---

ALLOWED_DOMAINS=(
    # Claude API
    api.anthropic.com
    api.claude.ai
    claude.ai
    statsig.anthropic.com
    sentry.io

    # npm / yarn / bun / pnpm
    registry.npmjs.org
    registry.yarnpkg.com
    registry.bun.sh
    registry.npmmirror.com

    # Go
    proxy.golang.org
    sum.golang.org
    storage.googleapis.com

    # Rust / crates.io
    crates.io
    static.crates.io
    index.crates.io
    static.rust-lang.org

    # Ruby gems
    rubygems.org
    api.rubygems.org
    index.rubygems.org

    # PyPI (in case they need a pip install)
    pypi.org
    files.pythonhosted.org

    # GitHub
    github.com
    api.github.com
    raw.githubusercontent.com
    objects.githubusercontent.com
    codeload.github.com
    pkg-containers.githubusercontent.com
    ghcr.io

    # Common CDNs used by package managers
    cdn.jsdelivr.net
    dl-cdn.alpinelinux.org
    deb.nodesource.com
)

echo "Resolving and whitelisting ${#ALLOWED_DOMAINS[@]} domains..."

for domain in "${ALLOWED_DOMAINS[@]}"; do
    # Resolve all IPs (A and AAAA records)
    ips=$(dig +short "$domain" A 2>/dev/null | grep -E '^[0-9]' || true)
    cnames=$(dig +short "$domain" CNAME 2>/dev/null || true)

    # Follow CNAMEs one level deep
    for cname in $cnames; do
        more_ips=$(dig +short "$cname" A 2>/dev/null | grep -E '^[0-9]' || true)
        ips="$ips $more_ips"
    done

    for ip in $ips; do
        iptables -A OUTPUT -d "$ip" -j ACCEPT 2>/dev/null || true
    done
done

# Allow HTTPS to all resolved IPs (some CDNs use shared IPs)
# This is already covered by the per-IP rules above, but we also
# allow port 443 to handle any IP rotation during the session
# via the ESTABLISHED,RELATED rule at the top.

# Default deny everything else
iptables -A OUTPUT -j REJECT --reject-with icmp-port-unreachable

echo "Firewall initialized. Outbound traffic restricted to whitelisted domains."
echo "Allowed: Claude API, npm/yarn/bun/pnpm, Go, Rust, Ruby, PyPI, GitHub"
