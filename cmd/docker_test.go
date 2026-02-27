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
		{"/home/user/projects/myapp", "sandbox-myapp"},
		{"/tmp/test", "sandbox-test"},
		{"/home/user/my-project", "sandbox-my-project"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := ContainerName(tt.path)
			if got != tt.want {
				t.Errorf("ContainerName(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestContainerNameCollision(t *testing.T) {
	// Two different paths with the same basename produce the same container
	// name. This documents a known limitation — callers should be aware.
	a := ContainerName("/projects/a/myapp")
	b := ContainerName("/projects/b/myapp")
	if a != b {
		t.Fatalf("expected collision but got %q vs %q", a, b)
	}
}

func TestContainerNameRootPath(t *testing.T) {
	// filepath.Base("/") returns "/" which produces an invalid Docker
	// container name. Document this edge case.
	got := ContainerName("/")
	if got != "sandbox-/" {
		t.Errorf("ContainerName(%q) = %q, want %q", "/", got, "sandbox-/")
	}
	// NOTE: Docker would reject this name at runtime. The production code
	// does not guard against this because resolvePath always produces a
	// path deeper than "/".
}

func TestResolvePath(t *testing.T) {
	// Relative path should resolve to absolute
	got := ResolvePath(".")
	if !filepath.IsAbs(got) {
		t.Errorf("ResolvePath(\".\") = %q, want absolute path", got)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got != cwd {
		t.Errorf("ResolvePath(\".\") = %q, want %q", got, cwd)
	}

	// Absolute path should pass through
	got = ResolvePath("/tmp/test")
	if got != "/tmp/test" {
		t.Errorf("ResolvePath(\"/tmp/test\") = %q, want \"/tmp/test\"", got)
	}
}

func TestResolvePathRelativeSubdir(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	got := ResolvePath("subdir/nested")
	want := filepath.Join(cwd, "subdir/nested")
	if got != want {
		t.Errorf("ResolvePath(\"subdir/nested\") = %q, want %q", got, want)
	}
}

func TestContainerNameSpecialChars(t *testing.T) {
	// Paths with dots, underscores, and numbers produce valid-looking names.
	tests := []struct {
		path string
		want string
	}{
		{"/home/user/my.project", "sandbox-my.project"},
		{"/home/user/my_project", "sandbox-my_project"},
		{"/home/user/v2.1.0", "sandbox-v2.1.0"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := ContainerName(tt.path)
			if got != tt.want {
				t.Errorf("ContainerName(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestContainerNameWithSpaces(t *testing.T) {
	// Paths with spaces produce container names with spaces, which Docker
	// would reject. Documents this limitation.
	got := ContainerName("/home/user/my project")
	if got != "sandbox-my project" {
		t.Errorf("containerName with spaces = %q, want %q", got, "sandbox-my project")
	}
	// NOTE: Docker rejects container names with spaces at runtime.
}

func TestFindSandboxRoot(t *testing.T) {
	t.Run("found in current dir", func(t *testing.T) {
		dir := t.TempDir()
		os.MkdirAll(filepath.Join(dir, ".sandbox"), 0755)

		got := FindSandboxRoot(dir)
		if got != dir {
			t.Errorf("FindSandboxRoot(%q) = %q, want %q", dir, got, dir)
		}
	})

	t.Run("found in parent", func(t *testing.T) {
		parent := t.TempDir()
		os.MkdirAll(filepath.Join(parent, ".sandbox"), 0755)
		child := filepath.Join(parent, "worktree", "feature-x")
		os.MkdirAll(child, 0755)

		got := FindSandboxRoot(child)
		if got != parent {
			t.Errorf("FindSandboxRoot(%q) = %q, want %q", child, got, parent)
		}
	})

	t.Run("found in grandparent", func(t *testing.T) {
		gp := t.TempDir()
		os.MkdirAll(filepath.Join(gp, ".sandbox"), 0755)
		child := filepath.Join(gp, "a", "b", "c")
		os.MkdirAll(child, 0755)

		got := FindSandboxRoot(child)
		if got != gp {
			t.Errorf("FindSandboxRoot(%q) = %q, want %q", child, got, gp)
		}
	})

	t.Run("closest parent wins", func(t *testing.T) {
		gp := t.TempDir()
		os.MkdirAll(filepath.Join(gp, ".sandbox"), 0755)
		parent := filepath.Join(gp, "sub")
		os.MkdirAll(filepath.Join(parent, ".sandbox"), 0755)
		child := filepath.Join(parent, "deep")
		os.MkdirAll(child, 0755)

		got := FindSandboxRoot(child)
		if got != parent {
			t.Errorf("FindSandboxRoot(%q) = %q, want %q (closest parent)", child, got, parent)
		}
	})

	t.Run("skips home directory", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)
		os.MkdirAll(filepath.Join(tmpHome, ".sandbox"), 0755)

		// Child under home with no .sandbox/ of its own
		child := filepath.Join(tmpHome, "projects", "foo")
		os.MkdirAll(child, 0755)

		got := FindSandboxRoot(child)
		if got != "" {
			t.Errorf("findSandboxRoot should skip ~/.sandbox, got %q", got)
		}
	})

	t.Run("none found", func(t *testing.T) {
		dir := t.TempDir()
		child := filepath.Join(dir, "a", "b")
		os.MkdirAll(child, 0755)

		got := FindSandboxRoot(child)
		if got != "" {
			t.Errorf("FindSandboxRoot(%q) = %q, want empty", child, got)
		}
	})
}

func TestResolveWorkspace(t *testing.T) {
	t.Run("no parent sandbox", func(t *testing.T) {
		dir := t.TempDir()
		child := filepath.Join(dir, "project")
		os.MkdirAll(child, 0755)

		root, workDir := ResolveWorkspace(child)
		if root != child {
			t.Errorf("sandboxRoot = %q, want %q", root, child)
		}
		if workDir != child {
			t.Errorf("workDir = %q, want %q", workDir, child)
		}
	})

	t.Run("parent sandbox found", func(t *testing.T) {
		parent := t.TempDir()
		os.MkdirAll(filepath.Join(parent, ".sandbox"), 0755)
		child := filepath.Join(parent, "worktree", "feature")
		os.MkdirAll(child, 0755)

		root, workDir := ResolveWorkspace(child)
		if root != parent {
			t.Errorf("sandboxRoot = %q, want %q", root, parent)
		}
		if workDir != child {
			t.Errorf("workDir = %q, want %q", workDir, child)
		}
	})

	t.Run("same dir has sandbox", func(t *testing.T) {
		dir := t.TempDir()
		os.MkdirAll(filepath.Join(dir, ".sandbox"), 0755)

		root, workDir := ResolveWorkspace(dir)
		if root != dir {
			t.Errorf("sandboxRoot = %q, want %q", root, dir)
		}
		if workDir != dir {
			t.Errorf("workDir = %q, want %q", workDir, dir)
		}
	})

	t.Run("here flag skips search", func(t *testing.T) {
		parent := t.TempDir()
		os.MkdirAll(filepath.Join(parent, ".sandbox"), 0755)
		child := filepath.Join(parent, "sub")
		os.MkdirAll(child, 0755)

		flagHere = true
		defer func() { flagHere = false }()

		root, workDir := ResolveWorkspace(child)
		if root != child {
			t.Errorf("with --here: sandboxRoot = %q, want %q", root, child)
		}
		if workDir != child {
			t.Errorf("with --here: workDir = %q, want %q", workDir, child)
		}
	})
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
