package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerName(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/home/user/projects/myapp", "ao-sandbox-myapp"},
		{"/tmp/test", "ao-sandbox-test"},
		{"/home/user/my-project", "ao-sandbox-my-project"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := containerName(tt.path)
			if got != tt.want {
				t.Errorf("containerName(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestContainerNameCollision(t *testing.T) {
	// Two different paths with the same basename produce the same container
	// name. This documents a known limitation — callers should be aware.
	a := containerName("/projects/a/myapp")
	b := containerName("/projects/b/myapp")
	if a != b {
		t.Fatalf("expected collision but got %q vs %q", a, b)
	}
}

func TestContainerNameRootPath(t *testing.T) {
	// filepath.Base("/") returns "/" which produces an invalid Docker
	// container name. Document this edge case.
	got := containerName("/")
	if got != "ao-sandbox-/" {
		t.Errorf("containerName(%q) = %q, want %q", "/", got, "ao-sandbox-/")
	}
	// NOTE: Docker would reject this name at runtime. The production code
	// does not guard against this because resolvePath always produces a
	// path deeper than "/".
}

func TestResolvePath(t *testing.T) {
	// Relative path should resolve to absolute
	got := resolvePath(".")
	if !filepath.IsAbs(got) {
		t.Errorf("resolvePath(\".\") = %q, want absolute path", got)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got != cwd {
		t.Errorf("resolvePath(\".\") = %q, want %q", got, cwd)
	}

	// Absolute path should pass through
	got = resolvePath("/tmp/test")
	if got != "/tmp/test" {
		t.Errorf("resolvePath(\"/tmp/test\") = %q, want \"/tmp/test\"", got)
	}
}

func TestResolvePathRelativeSubdir(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	got := resolvePath("subdir/nested")
	want := filepath.Join(cwd, "subdir/nested")
	if got != want {
		t.Errorf("resolvePath(\"subdir/nested\") = %q, want %q", got, want)
	}
}

func TestContainerNameSpecialChars(t *testing.T) {
	// Paths with dots, underscores, and numbers produce valid-looking names.
	tests := []struct {
		path string
		want string
	}{
		{"/home/user/my.project", "ao-sandbox-my.project"},
		{"/home/user/my_project", "ao-sandbox-my_project"},
		{"/home/user/v2.1.0", "ao-sandbox-v2.1.0"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := containerName(tt.path)
			if got != tt.want {
				t.Errorf("containerName(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestContainerNameWithSpaces(t *testing.T) {
	// Paths with spaces produce container names with spaces, which Docker
	// would reject. Documents this limitation.
	got := containerName("/home/user/my project")
	if got != "ao-sandbox-my project" {
		t.Errorf("containerName with spaces = %q, want %q", got, "ao-sandbox-my project")
	}
	// NOTE: Docker rejects container names with spaces at runtime.
}

func TestBuildImageWritesFiles(t *testing.T) {
	// Verify embedded files are non-empty (they're baked in at compile time)
	if len(dockerfile) == 0 {
		t.Error("embedded Dockerfile is empty")
	}
	if len(firewallScript) == 0 {
		t.Error("embedded init-firewall.sh is empty")
	}
	if len(entrypointScript) == 0 {
		t.Error("embedded entrypoint.sh is empty")
	}
}

func TestDockerfileContent(t *testing.T) {
	content := string(dockerfile)

	required := []string{
		"FROM ubuntu:",
		"TARGETARCH",
		"claude-code",
		"init-firewall.sh",
		"entrypoint.sh",
		"WORKDIR /workspace",
		"sleep",
	}

	for _, s := range required {
		if !strings.Contains(content, s) {
			t.Errorf("Dockerfile missing expected content: %q", s)
		}
	}
}

func TestEntrypointScriptContent(t *testing.T) {
	content := string(entrypointScript)

	required := []string{
		"init-firewall.sh",
		"exec \"$@\"",
	}

	for _, s := range required {
		if !strings.Contains(content, s) {
			t.Errorf("entrypoint.sh missing expected content: %q", s)
		}
	}
}

func TestFirewallScriptContent(t *testing.T) {
	content := string(firewallScript)

	required := []string{
		"iptables",
		"api.anthropic.com",
		"registry.npmjs.org",
		"proxy.golang.org",
		"crates.io",
		"rubygems.org",
		"github.com",
		"REJECT",
	}

	for _, s := range required {
		if !strings.Contains(content, s) {
			t.Errorf("init-firewall.sh missing expected content: %q", s)
		}
	}
}

func TestFirewallScriptDNSAndLoopback(t *testing.T) {
	content := string(firewallScript)

	// DNS resolution must be allowed or nothing else works
	if !strings.Contains(content, "dport 53") {
		t.Error("firewall script missing DNS port 53 rule")
	}
	// Loopback must be allowed
	if !strings.Contains(content, "-o lo") {
		t.Error("firewall script missing loopback rule")
	}
	// Established connections must be allowed for response traffic
	if !strings.Contains(content, "ESTABLISHED") {
		t.Error("firewall script missing ESTABLISHED conntrack rule")
	}
}

func TestFirewallScriptAllEcosystems(t *testing.T) {
	content := string(firewallScript)

	// Verify every package ecosystem mentioned in the Dockerfile has
	// corresponding firewall whitelist entries.
	ecosystems := map[string][]string{
		"npm/yarn": {"registry.npmjs.org", "registry.yarnpkg.com"},
		"go":       {"proxy.golang.org", "sum.golang.org"},
		"rust":     {"crates.io", "static.crates.io"},
		"ruby":     {"rubygems.org"},
		"python":   {"pypi.org", "files.pythonhosted.org"},
		"github":   {"github.com", "api.github.com"},
		"claude":   {"api.anthropic.com"},
	}

	for eco, domains := range ecosystems {
		for _, d := range domains {
			if !strings.Contains(content, d) {
				t.Errorf("firewall missing %s domain: %q", eco, d)
			}
		}
	}
}

func TestDockerfileNonRootUser(t *testing.T) {
	content := string(dockerfile)

	// The sandbox should run as a non-root user for security
	if !strings.Contains(content, "useradd") {
		t.Error("Dockerfile missing non-root user creation")
	}
	if !strings.Contains(content, "USER agent") {
		t.Error("Dockerfile missing USER agent directive")
	}
}

func TestDockerfileLanguageToolchains(t *testing.T) {
	content := string(dockerfile)

	// Every major toolchain the sandbox advertises should be installed
	toolchains := map[string]string{
		"node":   "nodejs",
		"go":     "go.dev/dl/go",
		"rust":   "rustup.rs",
		"python": "python3",
		"ruby":   "ruby",
	}

	for name, marker := range toolchains {
		if !strings.Contains(content, marker) {
			t.Errorf("Dockerfile missing %s toolchain (expected %q)", name, marker)
		}
	}
}
