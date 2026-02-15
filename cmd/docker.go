package cmd

import (
	"bufio"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed image/Dockerfile
var dockerfile []byte

//go:embed image/init-firewall.sh
var firewallScript []byte

//go:embed image/entrypoint.sh
var entrypointScript []byte

//go:embed image/workflow-linux
var workflowBinary []byte

var (
	imageName = "ao-sandbox"
	credsVol  = "ao-sandbox-creds"
	labelSel  = "ao.sandbox=true"
	labelWs   = "ao.workspace"
)

func ensureRunning(wsPath string) (string, error) {
	name := containerName(wsPath)
	if isRunning(name) {
		if err := syncContainer(name, false); err != nil {
			return "", err
		}
		return name, nil
	}

	// Restart a stopped container
	if containerExists(name) {
		fmt.Printf("Restarting sandbox for %s...\n", wsPath)
		if err := dockerRun("start", name); err != nil {
			return "", fmt.Errorf("restart container: %w", err)
		}
		if err := syncContainer(name, false); err != nil {
			return "", err
		}
		return name, nil
	}

	if err := ensureImage(); err != nil {
		return "", err
	}

	fmt.Printf("Starting sandbox for %s...\n", wsPath)
	cmd := exec.Command("docker", "run", "-d",
		"--name", name,
		"--hostname", name,
		"--label", labelSel,
		"--label", labelWs+"="+wsPath,
		"--cap-add", "NET_ADMIN",
		"-v", credsVol+":/home/agent/.claude",
		"-v", wsPath+":"+wsPath,
		"-w", wsPath,
		imageName)
	// cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("start container: %w", err)
	}

	if err := syncContainer(name, false); err != nil {
		return "", err
	}
	return name, nil
}

func ensureImage() error {
	if imageExists() {
		return nil
	}
	fmt.Println("Building sandbox image (first time)...")
	return buildImage()
}

func buildImage() error {
	dir, err := os.MkdirTemp("", "ao-sandbox-build-*")
	if err != nil {
		return fmt.Errorf("mkdtemp: %w", err)
	}
	defer os.RemoveAll(dir)

	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), dockerfile, 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "init-firewall.sh"), firewallScript, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "entrypoint.sh"), entrypointScript, 0755); err != nil {
		return err
	}

	buildArgs := []string{"build", "-t", imageName}
	if theme := zshTheme(); theme != "" {
		buildArgs = append(buildArgs, "--build-arg", "ZSH_THEME="+theme)
		if tp := customThemePath(theme); tp != "" {
			data, err := os.ReadFile(tp)
			if err != nil {
				return fmt.Errorf("read custom theme: %w", err)
			}
			buildArgs = append(buildArgs, "--build-arg", "CUSTOM_THEME="+base64.StdEncoding.EncodeToString(data))
		}
	}
	buildArgs = append(buildArgs, dir)
	cmd := exec.Command("docker", buildArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}
	return nil
}

func dockerExec(container, workdir string, args ...string) error {
	cmdArgs := append([]string{"exec", "-it", "-w", workdir, container}, args...)
	cmd := exec.Command("docker", cmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return fmt.Errorf("exec: %w", err)
	}
	return nil
}

func isRunning(name string) bool {
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", name).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

func containerExists(name string) bool {
	return exec.Command("docker", "inspect", name).Run() == nil
}

func imageExists() bool {
	return exec.Command("docker", "image", "inspect", imageName).Run() == nil
}

func dockerRun(args ...string) error {
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func containerName(wsPath string) string {
	return "ao-sandbox-" + filepath.Base(wsPath)
}

// zshTheme returns the user's ZSH theme name. It checks the ZSH_THEME
// environment variable first, then falls back to parsing ~/.zshrc.
// ZSH_THEME is typically a shell variable (not exported), so child processes
// like this binary won't see it via os.Getenv.
func zshTheme() string {
	if t := os.Getenv("ZSH_THEME"); t != "" {
		return t
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	f, err := os.Open(filepath.Join(home, ".zshrc"))
	if err != nil {
		return ""
	}
	defer f.Close()
	re := regexp.MustCompile(`^ZSH_THEME="(.+)"`)
	s := bufio.NewScanner(f)
	for s.Scan() {
		if m := re.FindStringSubmatch(s.Text()); m != nil {
			return m[1]
		}
	}
	return ""
}

func customThemePath(theme string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	p := filepath.Join(home, ".oh-my-zsh", "custom", "themes", theme+".zsh-theme")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

func resolvePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sandbox: resolve path: %v\n", err)
		os.Exit(1)
	}
	return abs
}

// copyToContainer writes data to a host temp file and docker-cp's it into the container.
func copyToContainer(container string, data []byte, dest string) error {
	tmp, err := os.CreateTemp("", "ao-sandbox-sync-*")
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

// syncHash computes a SHA-256 hash of all content that syncContainer pushes
// into the sandbox: workflow binary, entrypoint, firewall, ZSH theme config,
// and any custom theme file.
func syncHash() string {
	h := sha256.New()
	h.Write(workflowBinary)
	h.Write(entrypointScript)
	h.Write(firewallScript)
	theme := zshTheme()
	h.Write([]byte(theme))
	if theme != "" {
		if tp := customThemePath(theme); tp != "" {
			data, _ := os.ReadFile(tp)
			h.Write(data)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// syncContainer pushes the workflow binary, entrypoint, firewall script, and
// ZSH theme into a running container. It skips the sync when the container's
// /opt/ao-sync.sha256 already matches the current hash, unless force is true.
func syncContainer(name string, force bool) error {
	hash := syncHash()

	if !force {
		out, err := exec.Command("docker", "exec", name, "cat", "/opt/ao-sync.sha256").Output()
		if err == nil && strings.TrimSpace(string(out)) == hash {
			return nil
		}
	}

	fmt.Println("Syncing sandbox...")

	if err := copyToContainer(name, workflowBinary, "/usr/local/bin/workflow"); err != nil {
		return fmt.Errorf("sync workflow binary: %w", err)
	}
	if err := copyToContainer(name, entrypointScript, "/opt/entrypoint.sh"); err != nil {
		return fmt.Errorf("sync entrypoint: %w", err)
	}
	if err := copyToContainer(name, firewallScript, "/opt/init-firewall.sh"); err != nil {
		return fmt.Errorf("sync firewall script: %w", err)
	}

	if theme := zshTheme(); theme != "" {
		sedCmd := fmt.Sprintf(`s/^ZSH_THEME=.*/ZSH_THEME="%s"/`, theme)
		if err := exec.Command("docker", "exec", name, "sed", "-i", sedCmd, "/home/agent/.zshrc").Run(); err != nil {
			return fmt.Errorf("sync ZSH_THEME: %w", err)
		}
		if tp := customThemePath(theme); tp != "" {
			data, err := os.ReadFile(tp)
			if err != nil {
				return fmt.Errorf("read custom theme: %w", err)
			}
			dest := fmt.Sprintf("/home/agent/.oh-my-zsh/custom/themes/%s.zsh-theme", theme)
			if err := copyToContainer(name, data, dest); err != nil {
				return fmt.Errorf("sync custom theme: %w", err)
			}
			if err := exec.Command("docker", "exec", "-u", "root", name, "chown", "agent:agent", dest).Run(); err != nil {
				return fmt.Errorf("chown custom theme: %w", err)
			}
		}
	}

	if err := exec.Command("docker", "exec", "-u", "root", name, "sh", "-c", fmt.Sprintf("echo %s > /opt/ao-sync.sha256", hash)).Run(); err != nil {
		return fmt.Errorf("write sync hash: %w", err)
	}

	return nil
}
