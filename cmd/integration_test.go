package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testImageName = "sandbox-test"

// Minimal test image that mirrors the sandbox structure (agent user, .zshrc)
// so syncContainer exercises the real code paths.
const testDockerfile = `FROM alpine:latest
RUN apk add --no-cache bash nodejs
RUN adduser -D -s /bin/sh agent \
    && mkdir -p /home/agent/.oh-my-zsh/custom/themes \
    && echo 'ZSH_THEME="robbyrussell"' > /home/agent/.zshrc \
    && chown -R agent:agent /home/agent
RUN printf '#!/bin/sh\nexit 0\n' > /opt/init-firewall.sh && chmod +x /opt/init-firewall.sh
CMD ["sleep", "infinity"]
`

func dockerAvailable() bool {
	return exec.Command("docker", "info").Run() == nil
}

func requireDocker(t *testing.T) {
	t.Helper()
	if !dockerAvailable() {
		t.Skip("skipping: docker is not available in this environment")
	}
}

// useTestImage swaps the package-level image name to an isolated test image
// and restores it when the test finishes.
func useTestImage(t *testing.T) {
	t.Helper()
	orig := imageName
	imageName = testImageName
	t.Cleanup(func() { imageName = orig })
}

// buildTestImage builds a minimal alpine-based image for fast integration tests.
func buildTestImage(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(dir+"/Dockerfile", []byte(testDockerfile), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("docker", "build",
		"--label", "sandbox.image.hash="+ImageHash(),
		"-t", testImageName, dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build test image: %v", err)
	}

	t.Cleanup(func() {
		exec.Command("docker", "rmi", "-f", testImageName).Run()
	})
}

// overrideEmbeddedFiles replaces the package-level embedded files with stubs
// so ensureImage won't try to rebuild the real sandbox image during tests.
func overrideEmbeddedFiles(t *testing.T) {
	t.Helper()
	origDockerfile := dockerfile
	origFirewall := firewallScript
	dockerfile = []byte(testDockerfile)
	firewallScript = []byte("#!/bin/sh\n")
	t.Cleanup(func() {
		dockerfile = origDockerfile
		firewallScript = origFirewall
	})
}

// useTestConfig creates a minimal sandbox config in a temp HOME so that
// loadConfig succeeds during integration tests.
func useTestConfig(t *testing.T) {
	t.Helper()
	origHome := os.Getenv("HOME")
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Symlink the Docker config so Docker can still find its socket/context
	// after HOME changes (needed for non-default Docker hosts like OrbStack).
	origDocker := filepath.Join(origHome, ".docker")
	if _, err := os.Stat(origDocker); err == nil {
		os.Symlink(origDocker, filepath.Join(tmpHome, ".docker"))
	}

	configDir := tmpHome + "/.sandbox"
	os.MkdirAll(configDir, 0755)
	os.WriteFile(configDir+"/config.yaml", []byte("sync: []\nenv: {}\nfirewall:\n  allow: []\n"), 0644)
}

// removeContainer removes a container by name, ignoring errors.
func removeContainer(t *testing.T, name string) {
	t.Helper()
	t.Cleanup(func() {
		exec.Command("docker", "rm", "-f", name).Run()
	})
}

func TestImageExists(t *testing.T) {
	requireDocker(t)
	useTestImage(t)

	// Before building, image should not exist
	if imageExists() {
		// Clean up stale test image
		exec.Command("docker", "rmi", "-f", testImageName).Run()
	}
	if imageExists() {
		t.Fatal("imageExists() = true before build")
	}

	// Build it
	buildTestImage(t)

	// Now it should exist
	if !imageExists() {
		t.Fatal("imageExists() = false after build")
	}
}

func TestContainerLifecycle(t *testing.T) {
	requireDocker(t)
	useTestImage(t)
	buildTestImage(t)

	wsPath := t.TempDir()
	name := ContainerName(wsPath)
	removeContainer(t, name)

	// Container should not exist yet
	if ContainerExists(name) {
		t.Fatal("container exists before start")
	}
	if IsRunning(name) {
		t.Fatal("container running before start")
	}

	// Start it
	err := DockerRun("run", "-d",
		"--name", name,
		"--label", LabelSel,
		"--label", LabelWs+"="+wsPath,
		"-v", wsPath+":"+wsPath,
		testImageName)
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}

	// Should be running
	if !ContainerExists(name) {
		t.Fatal("container does not exist after start")
	}
	if !IsRunning(name) {
		t.Fatal("container not running after start")
	}

	// Stop it — container should still exist but not be running
	if err := DockerRun("stop", name); err != nil {
		t.Fatalf("docker stop: %v", err)
	}
	if !ContainerExists(name) {
		t.Fatal("container should still exist after stop")
	}
	if IsRunning(name) {
		t.Fatal("container should not be running after stop")
	}

	// Remove it — container should be gone
	if err := DockerRun("rm", "-f", name); err != nil {
		t.Fatalf("docker rm: %v", err)
	}
	if ContainerExists(name) {
		t.Fatal("container exists after removal")
	}
}

func TestEnsureRunningIdempotent(t *testing.T) {
	requireDocker(t)
	useTestImage(t)
	overrideEmbeddedFiles(t)
	buildTestImage(t)
	useTestConfig(t)

	wsPath := t.TempDir()
	name := ContainerName(wsPath)
	removeContainer(t, name)

	// First call should start
	got, err := EnsureRunning(wsPath)
	if err != nil {
		t.Fatalf("first ensureRunning: %v", err)
	}
	if got != name {
		t.Errorf("ensureRunning returned %q, want %q", got, name)
	}

	// Second call should be a no-op and return the same name
	got2, err := EnsureRunning(wsPath)
	if err != nil {
		t.Fatalf("second ensureRunning: %v", err)
	}
	if got2 != name {
		t.Errorf("second ensureRunning returned %q, want %q", got2, name)
	}
}

func TestContainerExecSimple(t *testing.T) {
	requireDocker(t)
	useTestImage(t)
	buildTestImage(t)

	wsPath := t.TempDir()
	name := ContainerName(wsPath)
	removeContainer(t, name)

	err := DockerRun("run", "-d",
		"--name", name,
		"-v", wsPath+":"+wsPath,
		testImageName)
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}

	// Run a simple command inside the container
	out, err := exec.Command("docker", "exec", name, "echo", "hello").Output()
	if err != nil {
		t.Fatalf("docker exec: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "hello" {
		t.Errorf("exec output = %q, want \"hello\"", got)
	}
}

func TestContainerWorkspaceMount(t *testing.T) {
	requireDocker(t)
	useTestImage(t)
	buildTestImage(t)

	wsPath := t.TempDir()
	name := ContainerName(wsPath)
	removeContainer(t, name)

	// Write a file to the workspace
	if err := os.WriteFile(wsPath+"/testfile.txt", []byte("sandbox-test"), 0644); err != nil {
		t.Fatal(err)
	}

	err := DockerRun("run", "-d",
		"--name", name,
		"-v", wsPath+":"+wsPath,
		testImageName)
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}

	// Verify the file is visible inside the container
	out, err := exec.Command("docker", "exec", name, "cat", wsPath+"/testfile.txt").Output()
	if err != nil {
		t.Fatalf("docker exec cat: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "sandbox-test" {
		t.Errorf("file content = %q, want \"sandbox-test\"", got)
	}
}

func TestEnsureRunningRestartsStoppedContainer(t *testing.T) {
	requireDocker(t)
	useTestImage(t)
	overrideEmbeddedFiles(t)
	buildTestImage(t)
	useTestConfig(t)

	wsPath := t.TempDir()
	name := ContainerName(wsPath)
	removeContainer(t, name)

	// Start a container, write a marker file, then stop it
	err := DockerRun("run", "-d",
		"--name", name,
		"--label", LabelSel,
		"-v", wsPath+":"+wsPath,
		testImageName)
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}

	// Write a marker inside the container (not on the mounted volume)
	if out, err := exec.Command("docker", "exec", name,
		"sh", "-c", "echo restarted > /tmp/marker").CombinedOutput(); err != nil {
		t.Fatalf("docker exec write marker: %v\n%s", err, out)
	}

	if err := DockerRun("stop", name); err != nil {
		t.Fatalf("docker stop: %v", err)
	}

	// Container exists but is not running
	if !ContainerExists(name) {
		t.Fatal("stopped container should still exist")
	}
	if IsRunning(name) {
		t.Fatal("stopped container should not be running")
	}

	// ensureRunning should restart the same container, not replace it
	got, err := EnsureRunning(wsPath)
	if err != nil {
		t.Fatalf("ensureRunning after stop: %v", err)
	}
	if got != name {
		t.Errorf("ensureRunning returned %q, want %q", got, name)
	}
	if !IsRunning(name) {
		t.Fatal("container should be running after ensureRunning restarted it")
	}

	// The marker file should still exist, proving it was restarted not replaced
	out, err := exec.Command("docker", "exec", name, "cat", "/tmp/marker").Output()
	if err != nil {
		t.Fatalf("docker exec read marker: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "restarted" {
		t.Errorf("marker = %q, want \"restarted\"", got)
	}
}

func TestContainerWriteFromInsideVisibleOnHost(t *testing.T) {
	requireDocker(t)
	useTestImage(t)
	buildTestImage(t)

	wsPath := t.TempDir()
	name := ContainerName(wsPath)
	removeContainer(t, name)

	err := DockerRun("run", "-d",
		"--name", name,
		"-v", wsPath+":"+wsPath,
		testImageName)
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}

	// Write a file from inside the container
	out, err := exec.Command("docker", "exec", name,
		"sh", "-c", "echo from-container > "+wsPath+"/created.txt && cat "+wsPath+"/created.txt").Output()
	if err != nil {
		t.Fatalf("docker exec: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "from-container" {
		t.Errorf("exec output = %q, want \"from-container\"", got)
	}

	// Verify the file is visible on the host
	data, err := os.ReadFile(wsPath + "/created.txt")
	if err != nil {
		t.Fatalf("read host file: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "from-container" {
		t.Errorf("host file content = %q, want \"from-container\"", got)
	}
}

func TestContainerLabels(t *testing.T) {
	requireDocker(t)
	useTestImage(t)
	buildTestImage(t)

	wsPath := t.TempDir()
	name := ContainerName(wsPath)
	removeContainer(t, name)

	err := DockerRun("run", "-d",
		"--name", name,
		"--label", LabelSel,
		"--label", LabelWs+"="+wsPath,
		testImageName)
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}

	// Check labels via docker inspect
	out, err := exec.Command("docker", "inspect", "-f",
		`{{index .Config.Labels "sandbox.workspace"}}`, name).Output()
	if err != nil {
		t.Fatalf("docker inspect: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != wsPath {
		t.Errorf("workspace label = %q, want %q", got, wsPath)
	}
}

// startHostToolDaemon starts a daemon on a random port, registers a session
// with the given tools, and returns the session ID and port.
func startHostToolDaemon(t *testing.T, tools []HostTool) (sessionID string, port int) {
	t.Helper()

	wsPath := t.TempDir()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port = l.Addr().(*net.TCPAddr).Port
	l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go RunHostToolDaemon(ctx, port)
	t.Cleanup(cancel)

	// Wait for daemon to be ready.
	for i := 0; i < 40; i++ {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	sessionID = GenerateSessionID()
	if err := RegisterHostToolSession(port, sessionID, tools, wsPath); err != nil {
		t.Fatalf("register session: %v", err)
	}
	t.Cleanup(func() { UnregisterHostToolSession(port, sessionID) })

	return sessionID, port
}

// execDaemonTool sends an execute request to the daemon and returns the response.
func execDaemonTool(port int, sessionID, toolName string) (hostToolResponse, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return hostToolResponse{}, err
	}
	defer conn.Close()

	msg := hostToolMessage{Type: "execute", Session: sessionID, Command: toolName}
	data, _ := json.Marshal(msg)
	data = append(data, '\n')
	conn.Write(data)

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return hostToolResponse{}, fmt.Errorf("no response")
	}
	var resp hostToolResponse
	json.Unmarshal(scanner.Bytes(), &resp)
	return resp, nil
}

func TestHosttoolEndToEnd(t *testing.T) {
	tools := []HostTool{
		{Name: "greet", Cmd: "echo hello-from-host"},
	}
	sessionID, port := startHostToolDaemon(t, tools)

	resp, err := execDaemonTool(port, sessionID, "greet")
	if err != nil {
		t.Fatalf("execute greet: %v", err)
	}
	if resp.ExitCode != 0 {
		t.Fatalf("exit code = %d, output = %q", resp.ExitCode, resp.Output)
	}
	if got := strings.TrimSpace(resp.Output); got != "hello-from-host" {
		t.Errorf("output = %q, want %q", got, "hello-from-host")
	}
}

func TestHosttoolUnknownCommand(t *testing.T) {
	tools := []HostTool{
		{Name: "deploy", Cmd: "echo deploy"},
	}
	sessionID, port := startHostToolDaemon(t, tools)

	resp, err := execDaemonTool(port, sessionID, "bogus")
	if err != nil {
		t.Fatalf("execute bogus: %v", err)
	}
	if resp.ExitCode == 0 {
		t.Fatal("expected nonzero exit for unknown command")
	}
	if !strings.Contains(resp.Output, "unknown command") {
		t.Errorf("output = %q, want to contain 'unknown command'", resp.Output)
	}
}

func TestHosttoolConcurrent(t *testing.T) {
	tools := []HostTool{
		{Name: "echo-a", Cmd: "echo aaa"},
		{Name: "echo-b", Cmd: "echo bbb"},
		{Name: "echo-c", Cmd: "echo ccc"},
	}
	sessionID, port := startHostToolDaemon(t, tools)

	type result struct {
		name   string
		output string
		err    error
	}

	ch := make(chan result, 3)
	for _, toolName := range []string{"echo-a", "echo-b", "echo-c"} {
		go func(n string) {
			resp, err := execDaemonTool(port, sessionID, n)
			ch <- result{name: n, output: strings.TrimSpace(resp.Output), err: err}
		}(toolName)
	}

	expected := map[string]string{"echo-a": "aaa", "echo-b": "bbb", "echo-c": "ccc"}
	for i := 0; i < 3; i++ {
		r := <-ch
		if r.err != nil {
			t.Errorf("tool %s failed: %v (%s)", r.name, r.err, r.output)
			continue
		}
		if want := expected[r.name]; r.output != want {
			t.Errorf("tool %s output = %q, want %q", r.name, r.output, want)
		}
	}
}
