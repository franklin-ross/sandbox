package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestBuildFirewallRules(t *testing.T) {
	t.Run("v4 rules from resolved entries", func(t *testing.T) {
		domains := []resolvedEntry{
			{v4: []string{"1.2.3.4"}, ports: []int{80, 443}},
		}
		v4, _ := buildFirewallRules(domains, nil)
		rules := string(v4)
		if !strings.Contains(rules, "-A OUTPUT -d 1.2.3.4/32 -p tcp --dport 80 -j ACCEPT") {
			t.Errorf("missing v4 port 80 rule:\n%s", rules)
		}
		if !strings.Contains(rules, "-A OUTPUT -d 1.2.3.4/32 -p tcp --dport 443 -j ACCEPT") {
			t.Errorf("missing v4 port 443 rule:\n%s", rules)
		}
	})

	t.Run("v6 rules from resolved entries", func(t *testing.T) {
		domains := []resolvedEntry{
			{v6: []string{"::1"}, ports: []int{443}},
		}
		_, v6 := buildFirewallRules(domains, nil)
		rules := string(v6)
		if !strings.Contains(rules, "-A OUTPUT -d ::1/128 -p tcp --dport 443 -j ACCEPT") {
			t.Errorf("missing v6 rule:\n%s", rules)
		}
	})

	t.Run("mixed domains and CIDRs", func(t *testing.T) {
		domains := []resolvedEntry{
			{v4: []string{"10.0.0.1"}, ports: []int{443}},
		}
		cidrs := []FirewallEntry{
			{CIDR: "172.16.0.0/12"},
		}
		v4, _ := buildFirewallRules(domains, cidrs)
		rules := string(v4)
		if !strings.Contains(rules, "-A OUTPUT -d 10.0.0.1/32 -p tcp --dport 443 -j ACCEPT") {
			t.Errorf("missing domain rule:\n%s", rules)
		}
		if !strings.Contains(rules, "-A OUTPUT -d 172.16.0.0/12 -j ACCEPT") {
			t.Errorf("missing CIDR rule:\n%s", rules)
		}
	})

	t.Run("v4 only entries produce no v6 domain rules", func(t *testing.T) {
		domains := []resolvedEntry{
			{v4: []string{"1.2.3.4"}, ports: []int{80}},
		}
		_, v6 := buildFirewallRules(domains, nil)
		rules := string(v6)
		if strings.Contains(rules, "1.2.3.4") {
			t.Errorf("v6 rules should not contain v4 address:\n%s", rules)
		}
	})
}

func TestResolveFirewallEntriesAsync(t *testing.T) {
	t.Run("resolves localhost and sends progress", func(t *testing.T) {
		cfg := &SandboxConfig{
			Firewall: FirewallConfig{
				Allow: []FirewallEntry{
					{Domain: "localhost"},
				},
			},
		}

		resultCh, progressCh := resolveFirewallEntriesAsync(cfg)

		// Drain progress — should get "localhost"
		var domains []string
		for d := range progressCh {
			domains = append(domains, d)
		}
		if len(domains) != 1 || domains[0] != "localhost" {
			t.Errorf("progress = %v, want [localhost]", domains)
		}

		result := <-resultCh
		if len(result.domains) == 0 {
			t.Fatal("expected at least one resolved domain")
		}
		if len(result.domains[0].v4) == 0 && len(result.domains[0].v6) == 0 {
			t.Error("localhost should resolve to at least one IP")
		}
	})

	t.Run("CIDR entries passed through", func(t *testing.T) {
		cfg := &SandboxConfig{
			Firewall: FirewallConfig{
				Allow: []FirewallEntry{
					{CIDR: "10.0.0.0/8"},
				},
			},
		}

		resultCh, progressCh := resolveFirewallEntriesAsync(cfg)

		// No domains to resolve, so no progress
		for range progressCh {
			t.Error("unexpected progress for CIDR-only config")
		}

		result := <-resultCh
		if len(result.cidrs) != 1 {
			t.Fatalf("expected 1 CIDR, got %d", len(result.cidrs))
		}
		if result.cidrs[0].CIDR != "10.0.0.0/8" {
			t.Errorf("CIDR = %q, want 10.0.0.0/8", result.cidrs[0].CIDR)
		}
	})

	t.Run("unresolvable domain sends progress but skips result", func(t *testing.T) {
		cfg := &SandboxConfig{
			Firewall: FirewallConfig{
				Allow: []FirewallEntry{
					{Domain: "this-domain-does-not-exist-12345.invalid"},
				},
			},
		}

		resultCh, progressCh := resolveFirewallEntriesAsync(cfg)

		var domains []string
		for d := range progressCh {
			domains = append(domains, d)
		}
		if len(domains) != 1 {
			t.Errorf("expected 1 progress message, got %d", len(domains))
		}

		result := <-resultCh
		if len(result.domains) != 0 {
			t.Errorf("expected 0 resolved domains for invalid host, got %d", len(result.domains))
		}
	})

	t.Run("empty config completes immediately", func(t *testing.T) {
		cfg := &SandboxConfig{}

		resultCh, progressCh := resolveFirewallEntriesAsync(cfg)

		for range progressCh {
			t.Error("unexpected progress for empty config")
		}

		result := <-resultCh
		if len(result.domains) != 0 || len(result.cidrs) != 0 {
			t.Error("expected empty result for empty config")
		}
	})
}

func TestFirewallConfigHash(t *testing.T) {
	t.Run("same config produces same hash", func(t *testing.T) {
		cfg := &SandboxConfig{
			Firewall: FirewallConfig{
				Allow: []FirewallEntry{
					{Domain: "example.com"},
					{CIDR: "10.0.0.0/8", Ports: []int{443}},
				},
			},
		}
		h1 := firewallConfigHash(cfg)
		h2 := firewallConfigHash(cfg)
		if !bytes.Equal(h1, h2) {
			t.Error("same config should produce same hash")
		}
	})

	t.Run("different domain produces different hash", func(t *testing.T) {
		cfg1 := &SandboxConfig{
			Firewall: FirewallConfig{
				Allow: []FirewallEntry{{Domain: "a.com"}},
			},
		}
		cfg2 := &SandboxConfig{
			Firewall: FirewallConfig{
				Allow: []FirewallEntry{{Domain: "b.com"}},
			},
		}
		if bytes.Equal(firewallConfigHash(cfg1), firewallConfigHash(cfg2)) {
			t.Error("different domains should produce different hash")
		}
	})

	t.Run("different ports produce different hash", func(t *testing.T) {
		cfg1 := &SandboxConfig{
			Firewall: FirewallConfig{
				Allow: []FirewallEntry{{Domain: "a.com", Ports: []int{80}}},
			},
		}
		cfg2 := &SandboxConfig{
			Firewall: FirewallConfig{
				Allow: []FirewallEntry{{Domain: "a.com", Ports: []int{443}}},
			},
		}
		if bytes.Equal(firewallConfigHash(cfg1), firewallConfigHash(cfg2)) {
			t.Error("different ports should produce different hash")
		}
	})

	t.Run("empty config produces consistent hash", func(t *testing.T) {
		cfg := &SandboxConfig{}
		h1 := firewallConfigHash(cfg)
		h2 := firewallConfigHash(cfg)
		if !bytes.Equal(h1, h2) {
			t.Error("empty config hash should be consistent")
		}
	})
}
