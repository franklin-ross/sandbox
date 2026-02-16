package cmd

import (
	"bufio"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

//go:embed image/Dockerfile
var dockerfile []byte

//go:embed image/init-firewall.sh
var firewallScript []byte

//go:embed image/entrypoint.sh
var entrypointScript []byte

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

// syncStatus prints a status line that overwrites itself.
func syncStatus(msg string) {
	fmt.Fprintf(os.Stderr, "\r\033[K  \033[2m%s\033[0m", msg)
}

// syncStatusDone clears the status line.
func syncStatusDone() {
	fmt.Fprintf(os.Stderr, "\r\033[K")
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

// syncContainer builds the sync manifest from config and pushes all items into
// the container. It skips the sync when the computed hash matches the
// container's /opt/ao-sync.sha256, unless force is true.
func syncContainer(name, wsPath string, force bool) error {
	cfg, err := loadConfig(wsPath)
	if err != nil {
		return err
	}

	items, err := buildSyncManifest(cfg)
	if err != nil {
		return fmt.Errorf("build sync manifest: %w", err)
	}

	// Compute hash over all sync items
	h := sha256.New()
	for _, item := range items {
		h.Write(item.Data)
		h.Write([]byte(item.Dest))
	}
	hash := hex.EncodeToString(h.Sum(nil))

	if !force {
		out, err := exec.Command("docker", "exec", name, "cat", "/opt/ao-sync.sha256").Output()
		if err == nil && strings.TrimSpace(string(out)) == hash {
			return nil
		}
	}

	fmt.Println("Syncing sandbox...")

	// Capture old firewall rules to detect changes
	oldFirewall, _ := exec.Command("docker", "exec", name, "cat", "/opt/ao-firewall-rules.sh").Output()

	if err := syncItems(name, items); err != nil {
		return err
	}

	// Re-run firewall if rules changed
	newFirewallRules := generateFirewallRules(cfg)
	if string(oldFirewall) != string(newFirewallRules) {
		syncStatus("updating firewall rules...")
		cmd := exec.Command("docker", "exec", "-u", "root", name, "/opt/init-firewall.sh")
		done := make(chan error, 1)
		go func() { done <- cmd.Run() }()

		timer := time.NewTimer(3 * time.Second)
		select {
		case err := <-done:
			timer.Stop()
			syncStatusDone()
			if err != nil {
				fmt.Fprintf(os.Stderr, "sandbox: warning: firewall update failed: %v\n", err)
			}
		case <-timer.C:
			syncStatus("resolving firewall domains...")
			if err := <-done; err != nil {
				syncStatusDone()
				fmt.Fprintf(os.Stderr, "sandbox: warning: firewall update failed: %v\n", err)
			} else {
				syncStatusDone()
			}
		}
	}

	// Write sync hash
	if err := exec.Command("docker", "exec", "-u", "root", name, "sh", "-c", fmt.Sprintf("echo %s > /opt/ao-sync.sha256", hash)).Run(); err != nil {
		return fmt.Errorf("write sync hash: %w", err)
	}

	return nil
}
