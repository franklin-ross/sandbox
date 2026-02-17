package cmd

import (
	"os/exec"
	"strings"
	"testing"
)

const integrationImage = "ao-sandbox-integration-test"
const integrationContainer = "ao-sandbox-integration-test-ctr"

// buildRealImage builds the actual sandbox image from the image/ directory
// under a test pseudonym so it doesn't collide with the production image.
func buildRealImage(t *testing.T) {
	t.Helper()

	cmd := exec.Command("docker", "build", "-t", integrationImage, "image")
	cmd.Stdout = nil
	cmd.Stderr = nil
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build sandbox image: %v\n%s", err, out)
	}

	t.Cleanup(func() {
		exec.Command("docker", "rmi", "-f", integrationImage).Run()
	})
}

// startIntegrationContainer starts a container from the real image and
// returns its name. The container is removed on cleanup.
func startIntegrationContainer(t *testing.T) string {
	t.Helper()

	name := integrationContainer
	// Remove any leftover from a previous failed run.
	exec.Command("docker", "rm", "-f", name).Run()

	// --cap-add NET_ADMIN is required for the entrypoint firewall (iptables).
	out, err := exec.Command("docker", "run", "-d",
		"--name", name,
		"--cap-add", "NET_ADMIN",
		integrationImage).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}

	t.Cleanup(func() {
		exec.Command("docker", "rm", "-f", name).Run()
	})

	return name
}

// execInContainer runs a command inside the integration container and
// returns its combined output.
func execInContainer(t *testing.T, name string, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"exec", name}, args...)
	out, err := exec.Command("docker", cmdArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker exec %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestImageIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: image integration tests require a full docker build")
	}
	requireDocker(t)
	buildRealImage(t)
	ctr := startIntegrationContainer(t)

	t.Run("toolchains", func(t *testing.T) {
		tools := []struct {
			name string
			cmd  []string
		}{
			{"node", []string{"node", "--version"}},
			{"npm", []string{"npm", "--version"}},
			{"go", []string{"go", "version"}},
			{"rustc", []string{"rustc", "--version"}},
			{"cargo", []string{"cargo", "--version"}},
			{"python3", []string{"python3", "--version"}},
			{"ruby", []string{"ruby", "--version"}},
			{"task", []string{"task", "--version"}},
		}

		for _, tt := range tools {
			t.Run(tt.name, func(t *testing.T) {
				out := execInContainer(t, ctr, tt.cmd...)
				if out == "" {
					t.Errorf("%s returned empty output", tt.name)
				}
			})
		}
	})

	t.Run("base tools", func(t *testing.T) {
		tools := []struct {
			name string
			cmd  []string
		}{
			{"git", []string{"git", "--version"}},
			{"curl", []string{"curl", "--version"}},
			{"jq", []string{"jq", "--version"}},
			{"ripgrep", []string{"rg", "--version"}},
			{"zsh", []string{"zsh", "--version"}},
			{"tmux", []string{"tmux", "-V"}},
		}

		for _, tt := range tools {
			t.Run(tt.name, func(t *testing.T) {
				execInContainer(t, ctr, tt.cmd...)
			})
		}
	})

	t.Run("non-root user", func(t *testing.T) {
		out := execInContainer(t, ctr, "whoami")
		if out != "agent" {
			t.Errorf("whoami = %q, want \"agent\"", out)
		}
	})

	t.Run("claude dir exists", func(t *testing.T) {
		execInContainer(t, ctr, "test", "-d", "/home/agent/.claude")
	})

	t.Run("claude dir owned by agent", func(t *testing.T) {
		out := execInContainer(t, ctr, "stat", "-c", "%U", "/home/agent/.claude")
		if out != "agent" {
			t.Errorf("/home/agent/.claude owner = %q, want \"agent\"", out)
		}
	})

	t.Run("chrome", func(t *testing.T) {
		out := execInContainer(t, ctr, "sh", "-c", "$CHROME_BIN --version")
		if !strings.Contains(strings.ToLower(out), "chrom") {
			t.Errorf("unexpected browser version output: %q", out)
		}
	})

	t.Run("firewall script", func(t *testing.T) {
		execInContainer(t, ctr, "test", "-x", "/opt/init-firewall.sh")
	})

	t.Run("entrypoint script", func(t *testing.T) {
		execInContainer(t, ctr, "test", "-x", "/opt/entrypoint.sh")
	})
}
