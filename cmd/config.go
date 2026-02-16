package cmd

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SandboxConfig holds the user-editable sandbox configuration.
type SandboxConfig struct {
	Sync     []SyncRule        `yaml:"sync"`
	Env      map[string]string `yaml:"env"`
	Firewall FirewallConfig    `yaml:"firewall"`
}

// SyncRule describes a file to sync into the container.
type SyncRule struct {
	Src   string `yaml:"src"`
	Dest  string `yaml:"dest"`
	Mode  string `yaml:"mode"`
	Owner string `yaml:"owner"`
}

// FirewallConfig holds firewall allowlist rules.
type FirewallConfig struct {
	Allow []FirewallEntry `yaml:"allow"`
}

// FirewallEntry describes a single firewall allowlist entry.
type FirewallEntry struct {
	Domain string `yaml:"domain"`
	CIDR   string `yaml:"cidr"`
	Ports  []int  `yaml:"ports"`
}

// SyncItem is an internal type used by the sync pipeline.
type SyncItem struct {
	Data  []byte
	Dest  string
	Mode  string // "0644" or "0755"
	Owner string // "root:root" or "agent:agent"
}

const defaultConfigYAML = `# Sandbox configuration
# Global: ~/.ao/sandbox/config.yaml
# Per-workspace: <workspace>/.ao/sandbox/config.yaml

sync:
  # Sync custom oh-my-zsh themes from host
  - src: ~/.oh-my-zsh/custom/themes/*.zsh-theme
    dest: ~/.oh-my-zsh/custom/themes/

env: {}

firewall:
  allow:
    # Claude API
    - domain: api.anthropic.com
    - domain: claude.ai
    - domain: statsig.anthropic.com
    - domain: sentry.io

    # npm / yarn / pnpm
    - domain: registry.npmjs.org
    - domain: registry.yarnpkg.com
    - domain: repo.yarnpkg.com
    - domain: registry.npmmirror.com

    # Go
    - domain: proxy.golang.org
    - domain: sum.golang.org
    - domain: storage.googleapis.com

    # Rust / crates.io
    - domain: crates.io
    - domain: static.crates.io
    - domain: index.crates.io
    - domain: static.rust-lang.org

    # Ruby gems
    - domain: rubygems.org
    - domain: api.rubygems.org
    - domain: index.rubygems.org

    # PyPI
    - domain: pypi.org
    - domain: files.pythonhosted.org

    # GitHub
    - domain: github.com
    - domain: api.github.com
    - domain: raw.githubusercontent.com
    - domain: objects.githubusercontent.com
    - domain: codeload.github.com
    - domain: pkg-containers.githubusercontent.com
    - domain: ghcr.io

    # CDNs
    - domain: cdn.jsdelivr.net
    - domain: dl-cdn.alpinelinux.org
    - domain: deb.nodesource.com

    # Cypress
    - domain: download.cypress.io
    - domain: cdn.cypress.io
`

func parseConfigFile(path string) (*SandboxConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var cfg SandboxConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to parse %s: %v\n", path, err)
		return &SandboxConfig{}, nil
	}

	// Validate firewall entries
	var valid []FirewallEntry
	for _, e := range cfg.Firewall.Allow {
		if validateFirewallEntry(e) {
			valid = append(valid, e)
		}
	}
	cfg.Firewall.Allow = valid

	return &cfg, nil
}

func validateFirewallEntry(e FirewallEntry) bool {
	hasDomain := e.Domain != ""
	hasCIDR := e.CIDR != ""
	if hasDomain == hasCIDR {
		if hasDomain {
			fmt.Fprintf(os.Stderr, "warning: firewall entry has both domain and cidr, skipping\n")
		} else {
			fmt.Fprintf(os.Stderr, "warning: firewall entry has neither domain nor cidr, skipping\n")
		}
		return false
	}
	return true
}

func loadConfig(wsPath string) (*SandboxConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	}

	global, err := parseConfigFile(filepath.Join(home, ".ao", "sandbox", "config.yaml"))
	if err != nil {
		return nil, fmt.Errorf("load global config: %w", err)
	}

	ws, err := parseConfigFile(filepath.Join(wsPath, ".ao", "sandbox", "config.yaml"))
	if err != nil {
		return nil, fmt.Errorf("load workspace config: %w", err)
	}

	if global == nil && ws == nil {
		return nil, fmt.Errorf("no sandbox config found; run 'sandbox config init' to create one")
	}

	if global == nil {
		return ws, nil
	}
	if ws == nil {
		return global, nil
	}
	return mergeConfig(global, ws), nil
}

func mergeConfig(base, override *SandboxConfig) *SandboxConfig {
	result := &SandboxConfig{
		Env:      make(map[string]string),
		Firewall: FirewallConfig{},
	}

	// Env: override replaces base per-key
	for k, v := range base.Env {
		result.Env[k] = v
	}
	for k, v := range override.Env {
		result.Env[k] = v
	}

	// Sync: override replaces base rule with same dest
	destMap := make(map[string]SyncRule)
	var destOrder []string
	for _, r := range base.Sync {
		if _, exists := destMap[r.Dest]; !exists {
			destOrder = append(destOrder, r.Dest)
		}
		destMap[r.Dest] = r
	}
	for _, r := range override.Sync {
		if _, exists := destMap[r.Dest]; !exists {
			destOrder = append(destOrder, r.Dest)
		}
		destMap[r.Dest] = r
	}
	for _, dest := range destOrder {
		result.Sync = append(result.Sync, destMap[dest])
	}

	// Firewall: additive
	result.Firewall.Allow = append(result.Firewall.Allow, base.Firewall.Allow...)
	result.Firewall.Allow = append(result.Firewall.Allow, override.Firewall.Allow...)

	return result
}

// resolvedEntry holds a firewall entry with its pre-resolved IPs split by family.
type resolvedEntry struct {
	v4    []string
	v6    []string
	ports []int
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

// generateFirewallRules resolves domain IPs on the host and produces an
// iptables-restore format ruleset. iptables-restore applies all rules in a
// single kernel call, so the firewall is never in a partial state — even if
// the process is interrupted (ctrl+c), the old rules stay in place.
func generateFirewallRules(cfg *SandboxConfig) (v4, v6 []byte) {
	domains, cidrs := resolveFirewallEntries(cfg)

	var b4 strings.Builder
	writeRestoreRules(&b4, domains, cidrs, false)

	var b6 strings.Builder
	writeRestoreRules(&b6, domains, cidrs, true)

	return []byte(b4.String()), []byte(b6.String())
}

func generateEnvFile(env map[string]string) []byte {
	if len(env) == 0 {
		return nil
	}

	var b strings.Builder
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := env[k]
		if strings.HasPrefix(v, "$") {
			hostVar := v[1:]
			expanded := os.Getenv(hostVar)
			if expanded == "" {
				continue
			}
			v = expanded
		}
		b.WriteString(fmt.Sprintf("export %s=%s\n", k, shellQuote(v)))
	}

	out := b.String()
	if out == "" {
		return nil
	}
	return []byte(out)
}

func expandTilde(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

func expandContainerTilde(p string) string {
	if strings.HasPrefix(p, "~/") {
		return "/home/agent/" + p[2:]
	}
	return p
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func defaultZshrc() string {
	theme := zshTheme()
	if theme == "" {
		theme = "robbyrussell"
	}
	return fmt.Sprintf(`export ZSH="$HOME/.oh-my-zsh"
ZSH_THEME="%s"
plugins=(git npm yarn golang rust)
source $ZSH/oh-my-zsh.sh

# Files on the host in ~/.ao/sandbox/home/bin/ are synced to ~/bin
# in the container on start. They need to be linux binaries to run.
export PATH="$HOME/bin:$PATH"

# Tool completions
eval "$(task --completion zsh)"

# Sandbox environment (managed by sandbox sync)
[ -f ~/.ao-env ] && source ~/.ao-env
`, theme)
}

func buildSyncManifest(cfg *SandboxConfig) ([]SyncItem, error) {
	var items []SyncItem

	// 1. Embedded entrypoint
	items = append(items, SyncItem{
		Data:  entrypointScript,
		Dest:  "/opt/entrypoint.sh",
		Mode:  "0755",
		Owner: "root:root",
	})

	// 2. Embedded firewall script
	items = append(items, SyncItem{
		Data:  firewallScript,
		Dest:  "/opt/init-firewall.sh",
		Mode:  "0755",
		Owner: "root:root",
	})

	// 3. Generated firewall rules (IPv4 + IPv6)
	v4Rules, v6Rules := generateFirewallRules(cfg)
	items = append(items, SyncItem{
		Data:  v4Rules,
		Dest:  "/opt/ao-firewall-rules.sh",
		Mode:  "0755",
		Owner: "root:root",
	})
	items = append(items, SyncItem{
		Data:  v6Rules,
		Dest:  "/opt/ao-firewall-rules6.sh",
		Mode:  "0755",
		Owner: "root:root",
	})

	// 4. Generated env file
	if envData := generateEnvFile(cfg.Env); envData != nil {
		items = append(items, SyncItem{
			Data:  envData,
			Dest:  "/home/agent/.ao-env",
			Mode:  "0644",
			Owner: "agent:agent",
		})
	}

	// 5. Home directory files from ~/.ao/sandbox/home/
	home, err := os.UserHomeDir()
	if err == nil {
		homeDir := filepath.Join(home, ".ao", "sandbox", "home")
		if info, statErr := os.Stat(homeDir); statErr == nil && info.IsDir() {
			walkErr := filepath.Walk(homeDir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.IsDir() {
					return nil
				}
				rel, err := filepath.Rel(homeDir, path)
				if err != nil {
					return err
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				mode := "0644"
				if strings.HasPrefix(rel, "bin/") {
					mode = "0755"
				}
				items = append(items, SyncItem{
					Data:  data,
					Dest:  "/home/agent/" + rel,
					Mode:  mode,
					Owner: "agent:agent",
				})
				return nil
			})
			if walkErr != nil {
				return nil, fmt.Errorf("walk home dir: %w", walkErr)
			}
		}
	}

	// 8. Explicit sync rules from config
	for _, rule := range cfg.Sync {
		mode := rule.Mode
		if mode == "" {
			mode = "0644"
		}
		owner := rule.Owner
		if owner == "" {
			owner = "agent:agent"
		}

		src := expandTilde(rule.Src)
		dest := expandContainerTilde(rule.Dest)

		matches, err := filepath.Glob(src)
		if err != nil {
			return nil, fmt.Errorf("glob %q: %w", rule.Src, err)
		}
		if matches == nil {
			matches = []string{src}
		}

		for _, m := range matches {
			data, err := os.ReadFile(m)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: cannot read %s: %v\n", m, err)
				continue
			}
			d := dest
			if len(matches) > 1 {
				d = filepath.Join(dest, filepath.Base(m))
			}
			items = append(items, SyncItem{
				Data:  data,
				Dest:  d,
				Mode:  mode,
				Owner: owner,
			})
		}
	}

	return items, nil
}
