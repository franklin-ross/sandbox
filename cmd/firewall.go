package cmd

import (
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"strings"
)

// resolvedEntry holds a firewall entry with its pre-resolved IPs split by family.
type resolvedEntry struct {
	v4    []string
	v6    []string
	ports []int
}

// resolveResult holds the result of background DNS resolution.
type resolveResult struct {
	domains []resolvedEntry
	cidrs   []FirewallEntry
}

// resolveFirewallEntries resolves all domain entries and returns per-entry IP
// lists. CIDR entries are returned as-is.
func resolveFirewallEntries(cfg *SandboxConfig) (domains []resolvedEntry, cidrs []FirewallEntry) {
	for _, e := range cfg.Firewall.Allow {
		if e.Domain != "" {
			ports := e.Ports
			if len(ports) == 0 {
				ports = []int{80, 443}
			}
			ips, err := net.LookupHost(e.Domain)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: cannot resolve %s: %v\n", e.Domain, err)
				continue
			}
			var re resolvedEntry
			re.ports = ports
			for _, ip := range ips {
				parsed := net.ParseIP(ip)
				if parsed == nil || parsed.IsUnspecified() {
					continue
				}
				if parsed.To4() != nil {
					re.v4 = append(re.v4, ip)
				} else {
					re.v6 = append(re.v6, ip)
				}
			}
			domains = append(domains, re)
		}
		if e.CIDR != "" {
			cidrs = append(cidrs, e)
		}
	}
	return domains, cidrs
}

// resolveFirewallEntriesAsync starts DNS resolution in a background goroutine.
// Progress sends each domain name just before its lookup begins, so callers can
// display which domain is currently being resolved. Both channels are closed
// when resolution is complete.
func resolveFirewallEntriesAsync(cfg *SandboxConfig) (result <-chan resolveResult, progress <-chan string) {
	resultCh := make(chan resolveResult, 1)
	progressCh := make(chan string, len(cfg.Firewall.Allow))

	go func() {
		defer close(resultCh)
		defer close(progressCh)

		var domains []resolvedEntry
		var cidrs []FirewallEntry

		for _, e := range cfg.Firewall.Allow {
			if e.Domain != "" {
				progressCh <- e.Domain
				ports := e.Ports
				if len(ports) == 0 {
					ports = []int{80, 443}
				}
				ips, err := net.LookupHost(e.Domain)
				if err != nil {
					fmt.Fprintf(os.Stderr, "warning: cannot resolve %s: %v\n", e.Domain, err)
					continue
				}
				var re resolvedEntry
				re.ports = ports
				for _, ip := range ips {
					parsed := net.ParseIP(ip)
					if parsed == nil || parsed.IsUnspecified() {
						continue
					}
					if parsed.To4() != nil {
						re.v4 = append(re.v4, ip)
					} else {
						re.v6 = append(re.v6, ip)
					}
				}
				domains = append(domains, re)
			}
			if e.CIDR != "" {
				cidrs = append(cidrs, e)
			}
		}

		resultCh <- resolveResult{domains: domains, cidrs: cidrs}
	}()

	return resultCh, progressCh
}

// writeRestoreRules writes an iptables-restore format ruleset for one address
// family. isV6 controls the REJECT target (icmp vs icmp6).
func writeRestoreRules(b *strings.Builder, domains []resolvedEntry, cidrs []FirewallEntry, isV6 bool) {
	b.WriteString("*filter\n")
	b.WriteString(":INPUT ACCEPT [0:0]\n")
	b.WriteString(":FORWARD ACCEPT [0:0]\n")
	b.WriteString(":OUTPUT ACCEPT [0:0]\n")

	b.WriteString("-A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT\n")
	b.WriteString("-A OUTPUT -o lo -j ACCEPT\n")
	b.WriteString("-A OUTPUT -p udp --dport 53 -j ACCEPT\n")
	b.WriteString("-A OUTPUT -p tcp --dport 53 -j ACCEPT\n")

	mask := "/32"
	if isV6 {
		mask = "/128"
	}

	for _, re := range domains {
		ips := re.v4
		if isV6 {
			ips = re.v6
		}
		for _, ip := range ips {
			for _, port := range re.ports {
				b.WriteString(fmt.Sprintf("-A OUTPUT -d %s%s -p tcp --dport %d -j ACCEPT\n", ip, mask, port))
			}
		}
	}

	for _, e := range cidrs {
		if len(e.Ports) == 0 {
			b.WriteString(fmt.Sprintf("-A OUTPUT -d %s -j ACCEPT\n", e.CIDR))
		} else {
			for _, p := range e.Ports {
				b.WriteString(fmt.Sprintf("-A OUTPUT -d %s -p tcp --dport %d -j ACCEPT\n", e.CIDR, p))
			}
		}
	}

	reject := "icmp-port-unreachable"
	if isV6 {
		reject = "icmp6-port-unreachable"
	}
	b.WriteString(fmt.Sprintf("-A OUTPUT -j REJECT --reject-with %s\n", reject))
	b.WriteString("COMMIT\n")
}

// buildFirewallRules generates iptables-restore format rulesets from
// pre-resolved entries. Used by the sync pipeline after async resolution.
func buildFirewallRules(domains []resolvedEntry, cidrs []FirewallEntry) (v4, v6 []byte) {
	var b4 strings.Builder
	writeRestoreRules(&b4, domains, cidrs, false)

	var b6 strings.Builder
	writeRestoreRules(&b6, domains, cidrs, true)

	return []byte(b4.String()), []byte(b6.String())
}

// generateFirewallRules resolves domain IPs on the host and produces an
// iptables-restore format ruleset. Convenience wrapper that resolves
// synchronously — the sync pipeline uses resolveFirewallEntriesAsync instead.
func generateFirewallRules(cfg *SandboxConfig) (v4, v6 []byte) {
	domains, cidrs := resolveFirewallEntries(cfg)
	return buildFirewallRules(domains, cidrs)
}

// firewallConfigHash returns a deterministic hash of the firewall configuration
// (domain names, CIDRs, ports) without resolving DNS. This allows the sync
// skip check to work without network access.
func firewallConfigHash(cfg *SandboxConfig) []byte {
	h := sha256.New()
	for _, e := range cfg.Firewall.Allow {
		h.Write([]byte(e.Domain))
		h.Write([]byte(e.CIDR))
		for _, p := range e.Ports {
			fmt.Fprintf(h, "%d", p)
		}
	}
	return h.Sum(nil)
}
