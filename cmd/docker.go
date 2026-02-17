package cmd

import (
	"bufio"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

//go:embed image/Dockerfile
var dockerfile []byte

var (
	imageName = "ao-sandbox"
	credsVol  = "ao-sandbox-creds"
	labelSel  = "ao.sandbox=true"
	labelWs   = "ao.workspace"
)

// ensureStarted makes sure the container is running, creating or restarting it
// as needed. It does NOT sync — callers handle that.
func ensureStarted(wsPath string) (string, error) {
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
		"-e", fmt.Sprintf("HOST_UID=%d", os.Getuid()),
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

// ensureRunning starts the container if needed and syncs files into it.
func ensureRunning(wsPath string) (string, error) {
	name, err := ensureStarted(wsPath)
	if err != nil {
		return "", err
	}
	if err := syncContainer(name, wsPath, false); err != nil {
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

	cmd := exec.Command("docker", "build", "-t", imageName, dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}
	return nil
}

func dockerExec(container, workdir string, cfg *SandboxConfig, args ...string) error {
	cmdArgs := []string{"exec", "-it", "-w", workdir}

	// Pass through TERM so colors work in the container shell
	if term := os.Getenv("TERM"); term != "" {
		cmdArgs = append(cmdArgs, "-e", "TERM="+term)
	}

	if cfg != nil && len(cfg.Env) > 0 {
		keys := make([]string, 0, len(cfg.Env))
		for k := range cfg.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			v := cfg.Env[k]
			if strings.HasPrefix(v, "$") {
				expanded := os.Getenv(v[1:])
				if expanded == "" {
					continue
				}
				v = expanded
			}
			cmdArgs = append(cmdArgs, "-e", k+"="+v)
		}
	}

	cmdArgs = append(cmdArgs, container)
	cmdArgs = append(cmdArgs, args...)

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

func resolvePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sandbox: resolve path: %v\n", err)
		os.Exit(1)
	}
	return abs
}
