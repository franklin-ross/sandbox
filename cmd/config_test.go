package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseConfigFile(t *testing.T) {
	t.Run("valid YAML", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		os.WriteFile(path, []byte(`env:
  FOO: bar
firewall:
  allow:
    - domain: example.com
`), 0644)

		cfg, err := parseConfigFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Env["FOO"] != "bar" {
			t.Errorf("env FOO = %q, want %q", cfg.Env["FOO"], "bar")
		}
		if len(cfg.Firewall.Allow) != 1 {
			t.Fatalf("firewall.allow len = %d, want 1", len(cfg.Firewall.Allow))
		}
		if cfg.Firewall.Allow[0].Domain != "example.com" {
			t.Errorf("domain = %q, want %q", cfg.Firewall.Allow[0].Domain, "example.com")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		cfg, err := parseConfigFile("/nonexistent/config.yaml")
		if err != nil {
			t.Fatal(err)
		}
		if cfg != nil {
			t.Fatal("expected nil config for missing file")
		}
	})

	t.Run("malformed YAML", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		os.WriteFile(path, []byte("{{invalid yaml"), 0644)

		cfg, err := parseConfigFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg == nil {
			t.Fatal("expected non-nil config on malformed YAML")
		}
	})

	t.Run("sync rules with defaults", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		os.WriteFile(path, []byte(`sync:
  - src: /foo/bar
    dest: /opt/bar
`), 0644)

		cfg, err := parseConfigFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Sync) != 1 {
			t.Fatalf("sync len = %d, want 1", len(cfg.Sync))
		}
		if cfg.Sync[0].Src != "/foo/bar" {
			t.Errorf("src = %q, want /foo/bar", cfg.Sync[0].Src)
		}
	})

	t.Run("firewall with ports", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		os.WriteFile(path, []byte(`firewall:
  allow:
    - domain: example.com
      ports: [8080, 9090]
`), 0644)

		cfg, err := parseConfigFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Firewall.Allow[0].Ports) != 2 {
			t.Fatalf("ports len = %d, want 2", len(cfg.Firewall.Allow[0].Ports))
		}
		if cfg.Firewall.Allow[0].Ports[0] != 8080 {
			t.Errorf("port 0 = %d, want 8080", cfg.Firewall.Allow[0].Ports[0])
		}
	})
}

func TestMergeConfig(t *testing.T) {
	t.Run("env override", func(t *testing.T) {
		base := &SandboxConfig{
			Env: map[string]string{"A": "1", "B": "2"},
		}
		override := &SandboxConfig{
			Env: map[string]string{"B": "3", "C": "4"},
		}
		merged := mergeConfig(base, override)
		if merged.Env["A"] != "1" {
			t.Errorf("A = %q, want %q", merged.Env["A"], "1")
		}
		if merged.Env["B"] != "3" {
			t.Errorf("B = %q, want %q", merged.Env["B"], "3")
		}
		if merged.Env["C"] != "4" {
			t.Errorf("C = %q, want %q", merged.Env["C"], "4")
		}
	})

	t.Run("sync replace by dest", func(t *testing.T) {
		base := &SandboxConfig{
			Sync: []SyncRule{
				{Src: "/a", Dest: "/opt/x"},
				{Src: "/b", Dest: "/opt/y"},
			},
		}
		override := &SandboxConfig{
			Sync: []SyncRule{
				{Src: "/c", Dest: "/opt/x"},
			},
		}
		merged := mergeConfig(base, override)
		if len(merged.Sync) != 2 {
			t.Fatalf("sync len = %d, want 2", len(merged.Sync))
		}
		for _, r := range merged.Sync {
			if r.Dest == "/opt/x" && r.Src != "/c" {
				t.Errorf("/opt/x src = %q, want /c", r.Src)
			}
		}
	})

	t.Run("sync preserves order", func(t *testing.T) {
		base := &SandboxConfig{
			Sync: []SyncRule{
				{Src: "/a", Dest: "/opt/x"},
				{Src: "/b", Dest: "/opt/y"},
			},
		}
		override := &SandboxConfig{
			Sync: []SyncRule{
				{Src: "/c", Dest: "/opt/z"},
			},
		}
		merged := mergeConfig(base, override)
		if len(merged.Sync) != 3 {
			t.Fatalf("sync len = %d, want 3", len(merged.Sync))
		}
		if merged.Sync[0].Dest != "/opt/x" {
			t.Errorf("sync[0] dest = %q, want /opt/x", merged.Sync[0].Dest)
		}
		if merged.Sync[2].Dest != "/opt/z" {
			t.Errorf("sync[2] dest = %q, want /opt/z", merged.Sync[2].Dest)
		}
	})

	t.Run("firewall additive", func(t *testing.T) {
		base := &SandboxConfig{
			Firewall: FirewallConfig{
				Allow: []FirewallEntry{{Domain: "a.com"}},
			},
		}
		override := &SandboxConfig{
			Firewall: FirewallConfig{
				Allow: []FirewallEntry{{Domain: "b.com"}},
			},
		}
		merged := mergeConfig(base, override)
		if len(merged.Firewall.Allow) != 2 {
			t.Fatalf("firewall.allow len = %d, want 2", len(merged.Firewall.Allow))
		}
	})

	t.Run("nil env maps", func(t *testing.T) {
		base := &SandboxConfig{}
		override := &SandboxConfig{
			Env: map[string]string{"A": "1"},
		}
		merged := mergeConfig(base, override)
		if merged.Env["A"] != "1" {
			t.Errorf("A = %q, want %q", merged.Env["A"], "1")
		}
	})
}

func TestLoadConfig(t *testing.T) {
	t.Run("global only", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		configDir := filepath.Join(tmpHome, ".sandbox")
		os.MkdirAll(configDir, 0755)
		os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(`
env:
  TEST: global
firewall:
  allow:
    - domain: example.com`), 0644)

		cfg, err := loadConfig("/nonexistent")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Env["TEST"] != "global" {
			t.Errorf("env TEST = %q, want %q", cfg.Env["TEST"], "global")
		}
		if len(cfg.Firewall.Allow) != 1 {
			t.Errorf("expected 1 firewall entry, got %d", len(cfg.Firewall.Allow))
		}
	})

	t.Run("workspace override", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		// Global config
		configDir := filepath.Join(tmpHome, ".sandbox")
		os.MkdirAll(configDir, 0755)
		os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(`env:
  TEST: global
firewall:
  allow:
    - domain: global.example.com
`), 0644)

		wsPath := t.TempDir()
		wsConfigDir := filepath.Join(wsPath, ".sandbox")
		os.MkdirAll(wsConfigDir, 0755)
		os.WriteFile(filepath.Join(wsConfigDir, "config.yaml"), []byte(`env:
  TEST: workspace
firewall:
  allow:
    - domain: custom.example.com
`), 0644)

		cfg, err := loadConfig(wsPath)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Env["TEST"] != "workspace" {
			t.Errorf("env TEST = %q, want %q", cfg.Env["TEST"], "workspace")
		}
		found := false
		for _, e := range cfg.Firewall.Allow {
			if e.Domain == "custom.example.com" {
				found = true
				break
			}
		}
		if !found {
			t.Error("workspace firewall entry not found")
		}
	})

	t.Run("neither exists", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		_, err := loadConfig("/nonexistent")
		if err == nil {
			t.Fatal("expected error when no config exists")
		}
		if !strings.Contains(err.Error(), "sandbox config init") {
			t.Errorf("error should mention 'sandbox config init', got: %v", err)
		}
	})
}

func TestOnSyncHookParsing(t *testing.T) {
	t.Run("full hook", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		os.WriteFile(path, []byte(`on_sync:
  - cmd: npm install
    name: install deps
    root: true
`), 0644)

		cfg, err := parseConfigFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.OnSync) != 1 {
			t.Fatalf("on_sync len = %d, want 1", len(cfg.OnSync))
		}
		if cfg.OnSync[0].Cmd != "npm install" {
			t.Errorf("cmd = %q, want %q", cfg.OnSync[0].Cmd, "npm install")
		}
		if cfg.OnSync[0].Name != "install deps" {
			t.Errorf("name = %q, want %q", cfg.OnSync[0].Name, "install deps")
		}
		if !cfg.OnSync[0].Root {
			t.Error("root should be true")
		}
	})

	t.Run("minimal hook", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		os.WriteFile(path, []byte(`on_sync:
  - cmd: echo hello
`), 0644)

		cfg, err := parseConfigFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.OnSync) != 1 {
			t.Fatalf("on_sync len = %d, want 1", len(cfg.OnSync))
		}
		if cfg.OnSync[0].Name != "" {
			t.Errorf("name = %q, want empty", cfg.OnSync[0].Name)
		}
		if cfg.OnSync[0].Root {
			t.Error("root should default to false")
		}
	})

	t.Run("empty cmd rejected", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		os.WriteFile(path, []byte(`on_sync:
  - cmd: ""
    name: bad hook
  - cmd: echo ok
`), 0644)

		cfg, err := parseConfigFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.OnSync) != 1 {
			t.Fatalf("on_sync len = %d, want 1 (empty cmd should be filtered)", len(cfg.OnSync))
		}
		if cfg.OnSync[0].Cmd != "echo ok" {
			t.Errorf("cmd = %q, want %q", cfg.OnSync[0].Cmd, "echo ok")
		}
	})

	t.Run("whitespace-only cmd rejected", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		os.WriteFile(path, []byte(`on_sync:
  - cmd: "   "
`), 0644)

		cfg, err := parseConfigFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.OnSync) != 0 {
			t.Fatalf("on_sync len = %d, want 0 (whitespace cmd should be filtered)", len(cfg.OnSync))
		}
	})
}

func TestMergeOnSync(t *testing.T) {
	t.Run("additive global first then workspace", func(t *testing.T) {
		base := &SandboxConfig{
			OnSync: []OnSyncHook{
				{Cmd: "echo global", Name: "global hook"},
			},
		}
		override := &SandboxConfig{
			OnSync: []OnSyncHook{
				{Cmd: "echo workspace", Name: "ws hook"},
			},
		}
		merged := mergeConfig(base, override)
		if len(merged.OnSync) != 2 {
			t.Fatalf("on_sync len = %d, want 2", len(merged.OnSync))
		}
		if merged.OnSync[0].Cmd != "echo global" {
			t.Errorf("on_sync[0] cmd = %q, want %q", merged.OnSync[0].Cmd, "echo global")
		}
		if merged.OnSync[1].Cmd != "echo workspace" {
			t.Errorf("on_sync[1] cmd = %q, want %q", merged.OnSync[1].Cmd, "echo workspace")
		}
	})

	t.Run("empty base", func(t *testing.T) {
		base := &SandboxConfig{}
		override := &SandboxConfig{
			OnSync: []OnSyncHook{
				{Cmd: "echo ws"},
			},
		}
		merged := mergeConfig(base, override)
		if len(merged.OnSync) != 1 {
			t.Fatalf("on_sync len = %d, want 1", len(merged.OnSync))
		}
	})

	t.Run("empty override", func(t *testing.T) {
		base := &SandboxConfig{
			OnSync: []OnSyncHook{
				{Cmd: "echo global"},
			},
		}
		override := &SandboxConfig{}
		merged := mergeConfig(base, override)
		if len(merged.OnSync) != 1 {
			t.Fatalf("on_sync len = %d, want 1", len(merged.OnSync))
		}
	})
}

func TestFirewallEntryValidation(t *testing.T) {
	t.Run("valid domain", func(t *testing.T) {
		if !validateFirewallEntry(FirewallEntry{Domain: "example.com"}) {
			t.Error("domain-only entry should be valid")
		}
	})

	t.Run("valid cidr", func(t *testing.T) {
		if !validateFirewallEntry(FirewallEntry{CIDR: "10.0.0.0/8"}) {
			t.Error("cidr-only entry should be valid")
		}
	})

	t.Run("both domain and cidr", func(t *testing.T) {
		if validateFirewallEntry(FirewallEntry{Domain: "example.com", CIDR: "10.0.0.0/8"}) {
			t.Error("entry with both domain and cidr should be invalid")
		}
	})

	t.Run("neither domain nor cidr", func(t *testing.T) {
		if validateFirewallEntry(FirewallEntry{}) {
			t.Error("entry with neither domain nor cidr should be invalid")
		}
	})

	t.Run("domain with ports", func(t *testing.T) {
		if !validateFirewallEntry(FirewallEntry{Domain: "example.com", Ports: []int{8080}}) {
			t.Error("domain with ports should be valid")
		}
	})
}

func TestGenerateFirewallRules(t *testing.T) {
	t.Run("domains resolve to iptables-restore format", func(t *testing.T) {
		cfg := &SandboxConfig{
			Firewall: FirewallConfig{
				Allow: []FirewallEntry{
					{Domain: "localhost"},
				},
			},
		}
		v4, _ := generateFirewallRules(cfg)
		rules := string(v4)
		if !strings.Contains(rules, "-A OUTPUT -d 127.0.0.1/32 -p tcp --dport 80 -j ACCEPT") {
			t.Errorf("v4 rules missing localhost entry:\n%s", rules)
		}
		if !strings.Contains(rules, "-A OUTPUT -d 127.0.0.1/32 -p tcp --dport 443 -j ACCEPT") {
			t.Errorf("v4 rules missing localhost port 443:\n%s", rules)
		}
	})

	t.Run("domain with custom ports", func(t *testing.T) {
		cfg := &SandboxConfig{
			Firewall: FirewallConfig{
				Allow: []FirewallEntry{
					{Domain: "localhost", Ports: []int{8080}},
				},
			},
		}
		v4, _ := generateFirewallRules(cfg)
		rules := string(v4)
		if !strings.Contains(rules, "-A OUTPUT -d 127.0.0.1/32 -p tcp --dport 8080 -j ACCEPT") {
			t.Errorf("rules missing custom port entry:\n%s", rules)
		}
	})

	t.Run("cidr without ports", func(t *testing.T) {
		cfg := &SandboxConfig{
			Firewall: FirewallConfig{
				Allow: []FirewallEntry{
					{CIDR: "10.0.0.0/8"},
				},
			},
		}
		v4, _ := generateFirewallRules(cfg)
		rules := string(v4)
		if !strings.Contains(rules, "-A OUTPUT -d 10.0.0.0/8 -j ACCEPT") {
			t.Errorf("rules missing CIDR entry:\n%s", rules)
		}
	})

	t.Run("cidr with ports", func(t *testing.T) {
		cfg := &SandboxConfig{
			Firewall: FirewallConfig{
				Allow: []FirewallEntry{
					{CIDR: "10.0.0.0/8", Ports: []int{443, 8080}},
				},
			},
		}
		v4, _ := generateFirewallRules(cfg)
		rules := string(v4)
		if !strings.Contains(rules, "-A OUTPUT -d 10.0.0.0/8 -p tcp --dport 443") {
			t.Errorf("rules missing CIDR port 443:\n%s", rules)
		}
		if !strings.Contains(rules, "-A OUTPUT -d 10.0.0.0/8 -p tcp --dport 8080") {
			t.Errorf("rules missing CIDR port 8080:\n%s", rules)
		}
	})

	t.Run("empty config produces base rules and COMMIT", func(t *testing.T) {
		cfg := &SandboxConfig{}
		v4, v6 := generateFirewallRules(cfg)
		for _, rules := range []string{string(v4), string(v6)} {
			if !strings.Contains(rules, "*filter") {
				t.Error("rules should contain *filter header")
			}
			if !strings.Contains(rules, "COMMIT") {
				t.Error("rules should end with COMMIT")
			}
		}
		if !strings.Contains(string(v4), "icmp-port-unreachable") {
			t.Error("v4 rules should use icmp-port-unreachable")
		}
		if !strings.Contains(string(v6), "icmp6-port-unreachable") {
			t.Error("v6 rules should use icmp6-port-unreachable")
		}
	})

	t.Run("v6 rules contain IPv6 addresses", func(t *testing.T) {
		cfg := &SandboxConfig{
			Firewall: FirewallConfig{
				Allow: []FirewallEntry{
					{Domain: "localhost"},
				},
			},
		}
		_, v6 := generateFirewallRules(cfg)
		rules := string(v6)
		if !strings.Contains(rules, "/128") {
			t.Errorf("v6 rules should use /128 mask:\n%s", rules)
		}
	})
}

func TestGenerateEnvFile(t *testing.T) {
	t.Run("literal value", func(t *testing.T) {
		env := map[string]string{"FOO": "bar"}
		data := string(generateEnvFile(env))
		if !strings.Contains(data, "export FOO='bar'") {
			t.Errorf("env file missing FOO:\n%s", data)
		}
	})

	t.Run("dynamic var", func(t *testing.T) {
		t.Setenv("TEST_SANDBOX_VAR", "dynamic_value")

		env := map[string]string{"TOKEN": "$TEST_SANDBOX_VAR"}
		data := string(generateEnvFile(env))
		if !strings.Contains(data, "dynamic_value") {
			t.Errorf("env file missing expanded value:\n%s", data)
		}
	})

	t.Run("unset var omitted", func(t *testing.T) {
		env := map[string]string{"TOKEN": "$NONEXISTENT_TEST_VAR_12345"}
		data := string(generateEnvFile(env))
		if strings.Contains(data, "TOKEN") {
			t.Errorf("env file should omit unset var:\n%s", data)
		}
	})

	t.Run("empty map", func(t *testing.T) {
		data := generateEnvFile(map[string]string{})
		if data != nil {
			t.Errorf("expected nil for empty map, got %q", string(data))
		}
	})

	t.Run("sorted keys", func(t *testing.T) {
		env := map[string]string{"ZZZ": "last", "AAA": "first"}
		data := string(generateEnvFile(env))
		aIdx := strings.Index(data, "AAA")
		zIdx := strings.Index(data, "ZZZ")
		if aIdx >= zIdx {
			t.Errorf("expected AAA before ZZZ:\n%s", data)
		}
	})
}

func TestDefaultZshrc(t *testing.T) {
	t.Run("with theme", func(t *testing.T) {
		t.Setenv("ZSH_THEME", "agnoster")

		data := defaultZshrc()
		if !strings.Contains(data, `ZSH_THEME="agnoster"`) {
			t.Errorf("zshrc = %q, want agnoster theme", data)
		}
		if !strings.Contains(data, "source $ZSH/oh-my-zsh.sh") {
			t.Error("zshrc missing oh-my-zsh source")
		}
	})

	t.Run("default theme", func(t *testing.T) {
		t.Setenv("ZSH_THEME", "")
		t.Setenv("HOME", "/nonexistent-test-home")

		data := defaultZshrc()
		if !strings.Contains(data, `ZSH_THEME="robbyrussell"`) {
			t.Errorf("zshrc = %q, want robbyrussell default", data)
		}
	})
}

func TestBuildSyncManifest(t *testing.T) {
	t.Run("basic ordering", func(t *testing.T) {
		cfg := &SandboxConfig{
			Env: map[string]string{"FOO": "bar"},
		}

		items, err := buildSyncManifest(cfg)
		if err != nil {
			t.Fatal(err)
		}

		// Firewall rules are synced separately (in parallel with DNS),
		// so the manifest only has: entrypoint, firewall script, env.
		if len(items) < 3 {
			t.Fatalf("expected at least 3 items, got %d", len(items))
		}
		if items[0].Dest != "/opt/entrypoint.sh" {
			t.Errorf("item 0 dest = %q, want /opt/entrypoint.sh", items[0].Dest)
		}
		if items[0].Owner != "root:root" {
			t.Errorf("item 0 owner = %q, want root:root", items[0].Owner)
		}
		if items[1].Dest != "/opt/init-firewall.sh" {
			t.Errorf("item 1 dest = %q, want /opt/init-firewall.sh", items[1].Dest)
		}
		if items[2].Dest != "/home/agent/.sandbox-env" {
			t.Errorf("item 2 dest = %q, want /home/agent/.sandbox-env", items[2].Dest)
		}
		if items[2].Owner != "agent:agent" {
			t.Errorf("item 2 owner = %q, want agent:agent", items[2].Owner)
		}
	})

	t.Run("home dir files", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)
		t.Setenv("ZSH_THEME", "")

		homeDir := filepath.Join(tmpHome, ".sandbox", "home")
		os.MkdirAll(filepath.Join(homeDir, "bin"), 0755)
		os.WriteFile(filepath.Join(homeDir, "bin", "hello"), []byte("#!/bin/sh\necho hi"), 0755)
		os.WriteFile(filepath.Join(homeDir, ".gitconfig"), []byte("[user]\nname=test"), 0644)

		cfg := &SandboxConfig{}
		items, err := buildSyncManifest(cfg)
		if err != nil {
			t.Fatal(err)
		}

		var binItem, gitconfigItem *SyncItem
		for i := range items {
			switch items[i].Dest {
			case "/home/agent/bin/hello":
				binItem = &items[i]
			case "/home/agent/.gitconfig":
				gitconfigItem = &items[i]
			}
		}

		if binItem == nil {
			t.Fatal("missing bin/hello item")
		}
		if binItem.Mode != "0755" {
			t.Errorf("bin/hello mode = %q, want 0755", binItem.Mode)
		}
		if binItem.Owner != "agent:agent" {
			t.Errorf("bin/hello owner = %q, want agent:agent", binItem.Owner)
		}

		if gitconfigItem == nil {
			t.Fatal("missing .gitconfig item")
		}
		if gitconfigItem.Mode != "0644" {
			t.Errorf(".gitconfig mode = %q, want 0644", gitconfigItem.Mode)
		}
	})

	t.Run("glob expansion", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
		os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0644)

		t.Setenv("HOME", "/nonexistent-test-home")
		t.Setenv("ZSH_THEME", "")

		cfg := &SandboxConfig{
			Sync: []SyncRule{
				{Src: filepath.Join(dir, "*.txt"), Dest: "/opt/texts"},
			},
		}

		items, err := buildSyncManifest(cfg)
		if err != nil {
			t.Fatal(err)
		}

		var found int
		for _, item := range items {
			if strings.HasPrefix(item.Dest, "/opt/texts/") {
				found++
			}
		}
		if found != 2 {
			t.Errorf("found %d glob items, want 2", found)
		}
	})

	t.Run("sync rule defaults", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "test.sh"), []byte("#!/bin/sh"), 0644)

		t.Setenv("HOME", "/nonexistent-test-home")
		t.Setenv("ZSH_THEME", "")

		cfg := &SandboxConfig{
			Sync: []SyncRule{
				{Src: filepath.Join(dir, "test.sh"), Dest: "/opt/test.sh"},
			},
		}

		items, err := buildSyncManifest(cfg)
		if err != nil {
			t.Fatal(err)
		}

		var syncItem *SyncItem
		for i := range items {
			if items[i].Dest == "/opt/test.sh" {
				syncItem = &items[i]
			}
		}
		if syncItem == nil {
			t.Fatal("missing sync item")
		}
		if syncItem.Mode != "0644" {
			t.Errorf("default mode = %q, want 0644", syncItem.Mode)
		}
		if syncItem.Owner != "agent:agent" {
			t.Errorf("default owner = %q, want agent:agent", syncItem.Owner)
		}
	})
}
