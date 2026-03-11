package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultHostcmdPort is the default TCP port for the hostcmd daemon.
const DefaultHostcmdPort = 9847

// SandboxConfig holds the user-editable sandbox configuration.
type SandboxConfig struct {
	Sync         []SyncRule        `yaml:"sync"`
	Env          map[string]string `yaml:"env"`
	Firewall     FirewallConfig    `yaml:"firewall"`
	OnSync       []OnSyncHook      `yaml:"on_sync"`
	HostCommands []HostCommand     `yaml:"host_commands"`
	HostcmdPort  int               `yaml:"hostcmd_port"`
}

// HostCommand describes a command the agent can trigger on the host.
type HostCommand struct {
	Name string `yaml:"name"`
	Cmd  string `yaml:"cmd"`
}

// OnSyncHook describes a command to run inside the container after sync.
type OnSyncHook struct {
	Cmd  string `yaml:"cmd"`
	Name string `yaml:"name"`
	Root bool   `yaml:"root"`
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

const DefaultConfigYAML = `# Sandbox configuration
# Global: ~/.sandbox/config.yaml
# Per-workspace: <workspace>/.sandbox/config.yaml

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

# on_sync:
#   - cmd: npm install
#     name: install deps
#   - cmd: chmod 600 ~/.ssh/*
#     root: true

# host_commands:
#   - name: deploy
#     cmd: ./deploy.sh
#   - name: restart-db
#     cmd: systemctl restart postgres
# hostcmd_port: 9847
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

	// Validate host_commands
	seenCmds := make(map[string]bool)
	var validCmds []HostCommand
	for _, hc := range cfg.HostCommands {
		if strings.TrimSpace(hc.Name) == "" {
			fmt.Fprintf(os.Stderr, "warning: host_command with empty name, skipping\n")
			continue
		}
		if strings.TrimSpace(hc.Cmd) == "" {
			fmt.Fprintf(os.Stderr, "warning: host_command %q with empty cmd, skipping\n", hc.Name)
			continue
		}
		if seenCmds[hc.Name] {
			fmt.Fprintf(os.Stderr, "warning: duplicate host_command %q, skipping\n", hc.Name)
			continue
		}
		seenCmds[hc.Name] = true
		validCmds = append(validCmds, hc)
	}
	cfg.HostCommands = validCmds

	// Validate on_sync hooks
	var validHooks []OnSyncHook
	for _, h := range cfg.OnSync {
		if strings.TrimSpace(h.Cmd) == "" {
			fmt.Fprintf(os.Stderr, "warning: on_sync hook with empty cmd, skipping\n")
			continue
		}
		validHooks = append(validHooks, h)
	}
	cfg.OnSync = validHooks

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

func LoadConfig(wsPath string) (*SandboxConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	}

	global, err := parseConfigFile(filepath.Join(home, ".sandbox", "config.yaml"))
	if err != nil {
		return nil, fmt.Errorf("load global config: %w", err)
	}

	ws, err := parseConfigFile(filepath.Join(wsPath, ".sandbox", "config.yaml"))
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

	// OnSync: additive (global first, then workspace)
	result.OnSync = append(result.OnSync, base.OnSync...)
	result.OnSync = append(result.OnSync, override.OnSync...)

	// HostCommands: override replaces base by name (like sync by dest)
	cmdMap := make(map[string]HostCommand)
	var cmdOrder []string
	for _, hc := range base.HostCommands {
		if _, exists := cmdMap[hc.Name]; !exists {
			cmdOrder = append(cmdOrder, hc.Name)
		}
		cmdMap[hc.Name] = hc
	}
	for _, hc := range override.HostCommands {
		if _, exists := cmdMap[hc.Name]; !exists {
			cmdOrder = append(cmdOrder, hc.Name)
		}
		cmdMap[hc.Name] = hc
	}
	for _, name := range cmdOrder {
		result.HostCommands = append(result.HostCommands, cmdMap[name])
	}

	// HostcmdPort: workspace overrides global
	result.HostcmdPort = base.HostcmdPort
	if override.HostcmdPort != 0 {
		result.HostcmdPort = override.HostcmdPort
	}

	return result
}

// EffectiveHostcmdPort returns the configured port or the default.
func (c *SandboxConfig) EffectiveHostcmdPort() int {
	if c.HostcmdPort != 0 {
		return c.HostcmdPort
	}
	return DefaultHostcmdPort
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

func DefaultZshrc() string {
	theme := zshTheme()
	if theme == "" {
		theme = "robbyrussell"
	}
	return fmt.Sprintf(`export ZSH="$HOME/.oh-my-zsh"
ZSH_THEME="%s"
plugins=(git npm yarn golang rust)
source $ZSH/oh-my-zsh.sh

# Files on the host in ~/.sandbox/home/bin/ are synced to ~/bin
# in the container on start. They need to be linux binaries to run.
export PATH="$HOME/bin:$PATH"

# Rust
[ -f "$HOME/.cargo/env" ] && . "$HOME/.cargo/env"

# nvm
export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh"
[ -s "$NVM_DIR/bash_completion" ] && . "$NVM_DIR/bash_completion"

# Tool completions
eval "$(task --completion zsh)"

# Sandbox environment (managed by sandbox sync)
[ -f ~/.sandbox-env ] && source ~/.sandbox-env
`, theme)
}
