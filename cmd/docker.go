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
)

//go:embed image/Dockerfile
var dockerfile []byte

var (
	imageName = "sandbox"
	labelSel  = "sandbox.managed=true"
	labelWs   = "sandbox.workspace"
)

// ensureStarted makes sure the container is running, creating or restarting it
// as needed. It does NOT sync — callers handle that.
func ensureStarted(wsPath string) (string, error) {
	name := containerName(wsPath)

	if isRunning(name) || containerExists(name) {
		warnIfStale(name)
	}

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
	fmt.Println("Sandbox ready")
	return name, nil
}

// imageHash returns a hash of all inputs that affect the built image.
func imageHash() string {
	h := sha256.New()
	h.Write(dockerfile)
	h.Write(firewallScript)
	h.Write(entrypointScript)
	h.Write([]byte(fmt.Sprintf("uid=%d", os.Getuid())))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func ensureImage() error {
	hash := imageHash()
	if imageExists() {
		// Check if the image was built from the same inputs.
		out, err := exec.Command("docker", "inspect", "-f",
			`{{index .Config.Labels "sandbox.image.hash"}}`, imageName).Output()
		if err == nil && strings.TrimSpace(string(out)) == hash {
			return nil
		}
		fmt.Println("Sandbox image outdated, rebuilding...")
	} else {
		fmt.Println("Building sandbox image (first time)...")
	}
	return buildImage(hash)
}

func buildImage(hash string) error {
	dir, err := os.MkdirTemp("", "sandbox-build-*")
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

	cmd := exec.Command("docker", "build",
		"--progress=plain",
		"--build-arg", fmt.Sprintf("HOST_UID=%d", os.Getuid()),
		"--label", "sandbox.image.hash="+hash,
		"-t", imageName, dir)

	// Show build progress as a single updating status line.
	// Docker build with --progress=plain outputs steps to stderr.
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}
	go func() {
		s := bufio.NewScanner(stdout)
		for s.Scan() {
			showBuildStep(s.Text())
		}
	}()
	s := bufio.NewScanner(stderr)
	for s.Scan() {
		showBuildStep(s.Text())
	}
	syncStatusDone()
	if err := cmd.Wait(); err != nil {
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


// buildStepRe matches Docker build step lines like "#8 0.123 ..." or "#8 RUN ..."
var buildStepRe = regexp.MustCompile(`^#\d+\s+(?:\d+\.\d+\s+)?(.+)`)

// ansiRe strips ANSI escape sequences (cursor moves, clears, colors, etc.)
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\].*?\x07|\x1bc`)

// showBuildStep parses a Docker build output line and shows a condensed status.
func showBuildStep(line string) {
	line = ansiRe.ReplaceAllString(line, "")
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	// Show RUN/COPY/FROM commands as the status
	if m := buildStepRe.FindStringSubmatch(line); m != nil {
		text := m[1]
		// Truncate long lines
		if len(text) > 72 {
			text = text[:72] + "..."
		}
		syncStatus(text)
	}
}

// warnIfStale prints a warning if the container was created from an older image.
func warnIfStale(container string) {
	ctrImage, err := exec.Command("docker", "inspect", "-f", "{{.Image}}", container).Output()
	if err != nil {
		return
	}
	imgID, err := exec.Command("docker", "inspect", "-f", "{{.Id}}", imageName).Output()
	if err != nil {
		return
	}
	if strings.TrimSpace(string(ctrImage)) != strings.TrimSpace(string(imgID)) {
		fmt.Fprintf(os.Stderr, "warning: this project is using an outdated container. To update, run `sandbox rm <folder>` and then restart.\n")
	}
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
	return "sandbox-" + filepath.Base(wsPath)
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
