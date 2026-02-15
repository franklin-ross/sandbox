package cmd

import (
	"bufio"
	_ "embed"
	"encoding/base64"
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

//go:embed image/agent-linux
var agentBinary []byte

var (
	imageName = "ao-sandbox"
	credsVol  = "ao-sandbox-creds"
	labelSel  = "ao.sandbox=true"
	labelWs   = "ao.workspace"
)

func ensureRunning(wsPath string) (string, error) {
	name := containerName(wsPath)
	if isRunning(name) {
		return name, nil
	}

	// Restart a stopped container
	if containerExists(name) {
		fmt.Printf("Restarting sandbox for %s...\n", wsPath)
		if err := dockerRun("start", name); err != nil {
			return "", fmt.Errorf("restart container: %w", err)
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
	if err := os.WriteFile(filepath.Join(dir, "workflow"), workflowBinary, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "agent"), agentBinary, 0755); err != nil {
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
