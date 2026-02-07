package cmd

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const testImageName = "ao-sandbox-test"

func dockerAvailable() bool {
	return exec.Command("docker", "info").Run() == nil
}

func requireDocker(t *testing.T) {
	t.Helper()
	if !dockerAvailable() {
		t.Fatal("docker not available")
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
	df := []byte("FROM alpine:latest\nRUN apk add --no-cache bash\nCMD [\"sleep\", \"infinity\"]\n")
	if err := os.WriteFile(dir+"/Dockerfile", df, 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("docker", "build", "-t", testImageName, dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build test image: %v", err)
	}

	t.Cleanup(func() {
		exec.Command("docker", "rmi", "-f", testImageName).Run()
	})
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
	name := containerName(wsPath)
	removeContainer(t, name)

	// Container should not exist yet
	if containerExists(name) {
		t.Fatal("container exists before start")
	}
	if isRunning(name) {
		t.Fatal("container running before start")
	}

	// Start it
	err := dockerRun("run", "-d",
		"--name", name,
		"--label", labelSel,
		"--label", labelWs+"="+wsPath,
		"-v", wsPath+":"+wsPath,
		testImageName)
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}

	// Should be running
	if !containerExists(name) {
		t.Fatal("container does not exist after start")
	}
	if !isRunning(name) {
		t.Fatal("container not running after start")
	}

	// Stop it — container should still exist but not be running
	if err := dockerRun("stop", name); err != nil {
		t.Fatalf("docker stop: %v", err)
	}
	if !containerExists(name) {
		t.Fatal("container should still exist after stop")
	}
	if isRunning(name) {
		t.Fatal("container should not be running after stop")
	}

	// Remove it — container should be gone
	if err := dockerRun("rm", "-f", name); err != nil {
		t.Fatalf("docker rm: %v", err)
	}
	if containerExists(name) {
		t.Fatal("container exists after removal")
	}
}

func TestEnsureRunningIdempotent(t *testing.T) {
	requireDocker(t)
	useTestImage(t)
	buildTestImage(t)

	wsPath := t.TempDir()
	name := containerName(wsPath)
	removeContainer(t, name)

	// Override ensureImage to use the already-built test image
	origDockerfile := dockerfile
	origFirewall := firewallScript
	origEntrypoint := entrypointScript
	dockerfile = []byte("FROM alpine:latest\nCMD [\"sleep\", \"infinity\"]\n")
	firewallScript = []byte("#!/bin/sh\n")
	entrypointScript = []byte("#!/bin/sh\nexec \"$@\"\n")
	t.Cleanup(func() {
		dockerfile = origDockerfile
		firewallScript = origFirewall
		entrypointScript = origEntrypoint
	})

	// First call should start
	got, err := ensureRunning(wsPath)
	if err != nil {
		t.Fatalf("first ensureRunning: %v", err)
	}
	if got != name {
		t.Errorf("ensureRunning returned %q, want %q", got, name)
	}

	// Second call should be a no-op and return the same name
	got2, err := ensureRunning(wsPath)
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
	name := containerName(wsPath)
	removeContainer(t, name)

	err := dockerRun("run", "-d",
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
	name := containerName(wsPath)
	removeContainer(t, name)

	// Write a file to the workspace
	if err := os.WriteFile(wsPath+"/testfile.txt", []byte("sandbox-test"), 0644); err != nil {
		t.Fatal(err)
	}

	err := dockerRun("run", "-d",
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
	buildTestImage(t)

	wsPath := t.TempDir()
	name := containerName(wsPath)
	removeContainer(t, name)

	// Override embedded files so ensureImage uses our test image
	origDockerfile := dockerfile
	origFirewall := firewallScript
	origEntrypoint := entrypointScript
	dockerfile = []byte("FROM alpine:latest\nCMD [\"sleep\", \"infinity\"]\n")
	firewallScript = []byte("#!/bin/sh\n")
	entrypointScript = []byte("#!/bin/sh\nexec \"$@\"\n")
	t.Cleanup(func() {
		dockerfile = origDockerfile
		firewallScript = origFirewall
		entrypointScript = origEntrypoint
	})

	// Start a container, write a marker file, then stop it
	err := dockerRun("run", "-d",
		"--name", name,
		"--label", labelSel,
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

	if err := dockerRun("stop", name); err != nil {
		t.Fatalf("docker stop: %v", err)
	}

	// Container exists but is not running
	if !containerExists(name) {
		t.Fatal("stopped container should still exist")
	}
	if isRunning(name) {
		t.Fatal("stopped container should not be running")
	}

	// ensureRunning should restart the same container, not replace it
	got, err := ensureRunning(wsPath)
	if err != nil {
		t.Fatalf("ensureRunning after stop: %v", err)
	}
	if got != name {
		t.Errorf("ensureRunning returned %q, want %q", got, name)
	}
	if !isRunning(name) {
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
	name := containerName(wsPath)
	removeContainer(t, name)

	err := dockerRun("run", "-d",
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
	name := containerName(wsPath)
	removeContainer(t, name)

	err := dockerRun("run", "-d",
		"--name", name,
		"--label", labelSel,
		"--label", labelWs+"="+wsPath,
		testImageName)
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}

	// Check labels via docker inspect
	out, err := exec.Command("docker", "inspect", "-f",
		`{{index .Config.Labels "ao.workspace"}}`, name).Output()
	if err != nil {
		t.Fatalf("docker inspect: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != wsPath {
		t.Errorf("workspace label = %q, want %q", got, wsPath)
	}
}
