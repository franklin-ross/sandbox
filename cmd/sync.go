package cmd

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed image/init-firewall.sh
var firewallScript []byte

//go:embed image/hosttool-mcp
var hosttoolMCPScript []byte

// syncStatus prints a status line that overwrites itself.
func syncStatus(msg string) {
	fmt.Fprintf(os.Stderr, "\r\033[K  \033[2m%s\033[0m", msg)
}

// syncStatusDone clears the status line.
func syncStatusDone() {
	fmt.Fprintf(os.Stderr, "\r\033[K")
}

// copyToContainer writes data to a host temp file and docker-cp's it into the container.
func copyToContainer(container string, data []byte, dest string) error {
	tmp, err := os.CreateTemp("", "sandbox-sync-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if err := os.WriteFile(tmp.Name(), data, 0755); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0755); err != nil {
		return err
	}
	tmp.Close()

	return exec.Command("docker", "cp", tmp.Name(), container+":"+dest).Run()
}

// syncItems copies each SyncItem into the container and sets ownership/permissions.
func syncItems(container string, items []SyncItem) error {
	for _, item := range items {
		syncStatus(item.Dest)
		dir := filepath.Dir(item.Dest)
		if err := exec.Command("docker", "exec", "-u", "root", container, "mkdir", "-p", dir).Run(); err != nil {
			syncStatusDone()
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
		if err := copyToContainer(container, item.Data, item.Dest); err != nil {
			syncStatusDone()
			return fmt.Errorf("sync %s: %w", item.Dest, err)
		}
		if err := exec.Command("docker", "exec", "-u", "root", container, "chown", item.Owner, item.Dest).Run(); err != nil {
			syncStatusDone()
			return fmt.Errorf("chown %s: %w", item.Dest, err)
		}
		if err := exec.Command("docker", "exec", "-u", "root", container, "chmod", item.Mode, item.Dest).Run(); err != nil {
			syncStatusDone()
			return fmt.Errorf("chmod %s: %w", item.Dest, err)
		}
	}
	syncStatusDone()
	return nil
}

// buildSyncManifest builds the list of non-firewall items to sync into the
// container. Firewall rules are resolved and synced separately (in parallel)
// by SyncContainer.
func buildSyncManifest(cfg *SandboxConfig) ([]SyncItem, error) {
	var items []SyncItem

	// 1. Embedded firewall script
	items = append(items, SyncItem{
		Data:  firewallScript,
		Dest:  "/opt/init-firewall.sh",
		Mode:  "0755",
		Owner: "root:root",
	})

	// 3. Generated env file
	if envData := generateEnvFile(cfg.Env); envData != nil {
		items = append(items, SyncItem{
			Data:  envData,
			Dest:  "/home/agent/.sandbox-env",
			Mode:  "0644",
			Owner: "agent:agent",
		})
	}

	// 4. Home directory files from ~/.sandbox/home/
	home, err := os.UserHomeDir()
	if err == nil {
		homeDir := filepath.Join(home, ".sandbox", "home")
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

	// 5. Host tool files (only when host_tools are configured)
	if len(cfg.HostTools) > 0 {
		// 5a. Tool definitions JSON for the MCP server
		type toolDef struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		defs := make([]toolDef, len(cfg.HostTools))
		for i, ht := range cfg.HostTools {
			defs[i] = toolDef{Name: ht.Name, Description: ht.Description}
		}
		toolsJSON, _ := json.Marshal(defs)
		items = append(items, SyncItem{
			Data:  toolsJSON,
			Dest:  "/opt/sandbox-host-tools.json",
			Mode:  "0644",
			Owner: "root:root",
		})

		// 5b. MCP server script
		items = append(items, SyncItem{
			Data:  hosttoolMCPScript,
			Dest:  "/usr/local/bin/hosttool-mcp",
			Mode:  "0755",
			Owner: "root:root",
		})

	}

	// 6a. Claude settings.json (always synced — sandbox defaults + user overrides)
	settingsData, err := buildClaudeSettings()
	if err != nil {
		return nil, fmt.Errorf("build claude settings: %w", err)
	}
	items = append(items, SyncItem{
		Data:  settingsData,
		Dest:  "/home/agent/.claude/settings.json",
		Mode:  "0644",
		Owner: "agent:agent",
	})

	// 7. Explicit sync rules from config
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

// buildClaudeSettings reads the user's Claude settings from ~/.sandbox/home/.claude/settings.json
// (if it exists), merges in sandbox defaults, and returns the result.
func buildClaudeSettings() ([]byte, error) {
	settings := make(map[string]interface{})

	home, err := os.UserHomeDir()
	if err == nil {
		userSettings := filepath.Join(home, ".sandbox", "home", ".claude", "settings.json")
		if data, err := os.ReadFile(userSettings); err == nil {
			json.Unmarshal(data, &settings)
		}
	}

	// Skip the "are you sure?" prompt — the container IS the sandbox.
	settings["skipDangerousModePermissionPrompt"] = true

	return json.Marshal(settings)
}

// mergeClaudeJSON reads the existing /home/agent/.claude.json from inside the
// container, merges in the host-tools MCP server config, and writes it back.
// This avoids overwriting OAuth tokens and other data Claude Code stores there.
func mergeClaudeJSON(container string) error {
	claudeJSON := make(map[string]interface{})

	// Read whatever Claude Code has already written inside the container.
	out, err := exec.Command("docker", "exec", container, "cat", "/home/agent/.claude.json").Output()
	if err == nil {
		json.Unmarshal(out, &claudeJSON)
	}

	mcpServers, ok := claudeJSON["mcpServers"].(map[string]interface{})
	if !ok {
		mcpServers = make(map[string]interface{})
	}
	mcpServers["host-tools"] = map[string]interface{}{
		"command": "/usr/local/bin/hosttool-mcp",
	}
	claudeJSON["mcpServers"] = mcpServers

	data, err := json.Marshal(claudeJSON)
	if err != nil {
		return fmt.Errorf("marshal .claude.json: %w", err)
	}

	if err := copyToContainer(container, data, "/home/agent/.claude.json"); err != nil {
		return fmt.Errorf("write .claude.json: %w", err)
	}
	if err := exec.Command("docker", "exec", "-u", "root", container, "chown", "agent:agent", "/home/agent/.claude.json").Run(); err != nil {
		return fmt.Errorf("chown .claude.json: %w", err)
	}
	return nil
}

// SyncContainer builds the sync manifest and resolves firewall DNS in parallel,
// then pushes all items into the container and applies firewall rules.
func SyncContainer(name, wsPath string, force bool) error {
	cfg, err := LoadConfig(wsPath)
	if err != nil {
		return err
	}

	items, err := buildSyncManifest(cfg)
	if err != nil {
		return fmt.Errorf("build sync manifest: %w", err)
	}

	// Compute hash over sync items + firewall config + on_sync hooks.
	// This lets us skip sync without DNS when nothing has changed.
	h := sha256.New()
	for _, item := range items {
		h.Write(item.Data)
		h.Write([]byte(item.Dest))
	}
	h.Write(firewallConfigHash(cfg))
	for _, hook := range cfg.OnSync {
		h.Write([]byte(hook.Cmd))
		h.Write([]byte(hook.Name))
		if hook.Root {
			h.Write([]byte("root"))
		}
	}
	hash := hex.EncodeToString(h.Sum(nil))

	if !force {
		out, err := exec.Command("docker", "exec", name, "cat", "/opt/sandbox-sync.sha256").Output()
		if err == nil && strings.TrimSpace(string(out)) == hash {
			return nil
		}
	}

	fmt.Println("Syncing sandbox...")

	// Start DNS resolution in background while we sync files
	resultCh, progressCh := resolveFirewallEntriesAsync(cfg)

	// Capture old firewall rules to detect changes
	oldV4, _ := exec.Command("docker", "exec", name, "cat", "/opt/sandbox-firewall-rules.sh").Output()
	oldV6, _ := exec.Command("docker", "exec", name, "cat", "/opt/sandbox-firewall-rules6.sh").Output()

	// Sync non-firewall items (runs in parallel with DNS resolution)
	if err := syncItems(name, items); err != nil {
		return err
	}

	// Wait for DNS resolution, showing per-domain progress if still running
	var resolved resolveResult
	select {
	case resolved = <-resultCh:
		// DNS finished before or with file sync
	default:
		// DNS still running — show which domain we're resolving
		for domain := range progressCh {
			syncStatus("resolving " + domain)
		}
		resolved = <-resultCh
		syncStatusDone()
	}

	// Resolve host gateway from inside the container for host tool firewall rules.
	// host.docker.internal only resolves inside containers, not on the host.
	if len(cfg.HostTools) > 0 {
		if gw := resolveHostGateway(name, cfg.EffectiveHostToolPort()); gw != nil {
			resolved.domains = append(resolved.domains, *gw)
		}
	}

	// Generate firewall rules from resolved entries
	v4Rules, v6Rules := buildFirewallRules(resolved.domains, resolved.cidrs)

	// Sync firewall rules files
	fwItems := []SyncItem{
		{Data: v4Rules, Dest: "/opt/sandbox-firewall-rules.sh", Mode: "0755", Owner: "root:root"},
		{Data: v6Rules, Dest: "/opt/sandbox-firewall-rules6.sh", Mode: "0755", Owner: "root:root"},
	}
	if err := syncItems(name, fwItems); err != nil {
		return err
	}

	// Re-apply firewall if rules changed (atomic via iptables-restore)
	if string(oldV4) != string(v4Rules) || string(oldV6) != string(v6Rules) {
		syncStatus("applying firewall rules...")
		if err := exec.Command("docker", "exec", "-u", "root", name, "/opt/init-firewall.sh").Run(); err != nil {
			syncStatusDone()
			fmt.Fprintf(os.Stderr, "warning: firewall update failed: %v\n", err)
		}
		syncStatusDone()
	}

	// Merge MCP server config into .claude.json (reads existing file first to
	// preserve OAuth tokens and other data Claude Code stores there).
	if len(cfg.HostTools) > 0 {
		syncStatus("configuring MCP server...")
		if err := mergeClaudeJSON(name); err != nil {
			syncStatusDone()
			return err
		}
		syncStatusDone()
	}

	// Run on_sync hooks
	if err := runOnSyncHooks(name, "/home/agent", cfg.OnSync); err != nil {
		return err
	}

	// Write sync hash
	if err := exec.Command("docker", "exec", "-u", "root", name, "sh", "-c", fmt.Sprintf("echo %s > /opt/sandbox-sync.sha256", hash)).Run(); err != nil {
		return fmt.Errorf("write sync hash: %w", err)
	}

	return nil
}

// runOnSyncHooks executes on_sync hooks sequentially inside the container.
func runOnSyncHooks(container, workdir string, hooks []OnSyncHook) error {
	for _, hook := range hooks {
		label := hook.Name
		if label == "" {
			label = hook.Cmd
		}
		syncStatus("hook: " + label)
		user := "agent"
		if hook.Root {
			user = "root"
		}
		cmd := exec.Command("docker", "exec", "-u", user, "-w", workdir,
			container, "sh", "-c", hook.Cmd)
		output, err := cmd.CombinedOutput()
		if err != nil {
			syncStatusDone()
			return fmt.Errorf("on_sync hook %q failed: %w\n%s", label, err, string(output))
		}
	}
	syncStatusDone()
	return nil
}
