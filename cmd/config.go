package cmd

import (
	"fmt"
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
      ports: [22, 80, 443]
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

    # Playwright
    - domain: cdn.playwright.dev
    - domain: playwright.download.prss.microsoft.com
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
