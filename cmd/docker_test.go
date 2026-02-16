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

func TestNoDockerInDocker(t *testing.T) {
	dfContent := string(dockerfile)

	// The sandbox image must never install Docker tooling. Allowing
	// Docker-in-Docker would let a sandboxed process escape the container
	// by talking to the host daemon or launching sibling containers.
	forbiddenPackages := []string{
		"docker.io",
		"docker-ce",
		"docker-ce-cli",
		"containerd",
		"dockerd",
	}
	for _, pkg := range forbiddenPackages {
		if strings.Contains(dfContent, pkg) {
			t.Errorf("Dockerfile must not install %q — Docker-in-Docker is a container-escape vector", pkg)
		}
	}

	// Also verify the runtime configuration in docker.go doesn't enable DinD.
	goSource, err := os.ReadFile("docker.go")
	if err != nil {
		t.Fatalf("reading docker.go: %v", err)
	}
	goContent := string(goSource)

	// The host Docker socket must never be mounted into the container.
	if strings.Contains(goContent, "docker.sock") {
		t.Error("docker.go must not mount /var/run/docker.sock — Docker-in-Docker is a container-escape vector")
	}

	// --privileged grants full host device access, enabling DinD and
	// defeating every other sandbox restriction.
	if strings.Contains(goContent, "--privileged") {
		t.Error("docker.go must not use --privileged — it enables Docker-in-Docker and full host access")
	}
}
