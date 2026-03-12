package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// findFreePort returns a port the OS has confirmed is available.
func findFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// startTestDaemon starts a daemon on a free port and returns the port and a
// cancel function. The daemon runs in a background goroutine.
func startTestDaemon(t *testing.T) (int, context.CancelFunc) {
	t.Helper()
	port := findFreePort(t)
	ctx, cancel := context.WithCancel(context.Background())

	ready := make(chan struct{})
	go func() {
		// Tiny race window: bind the port in RunHostToolDaemon.
		// Signal ready once we know it's started (or errored).
		close(ready)
		RunHostToolDaemon(ctx, port)
	}()
	<-ready

	// Wait for the daemon to be connectable.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Cleanup(func() { cancel() })
			return port, cancel
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	t.Fatalf("daemon did not start on port %d", port)
	return 0, nil
}

func TestDaemonRegisterAndExecute(t *testing.T) {
	port, _ := startTestDaemon(t)

	sessionID := "test-session-1"
	tools := []HostTool{
		{Name: "hello", Cmd: "echo hello-world"},
	}
	if err := RegisterHostToolSession(port, sessionID, tools, t.TempDir()); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Execute the command via the protocol.
	resp := sendExecute(t, port, sessionID, "hello")
	if resp.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0", resp.ExitCode)
	}
	if !strings.Contains(resp.Output, "hello-world") {
		t.Errorf("output = %q, want to contain %q", resp.Output, "hello-world")
	}
}

func TestDaemonRejectUnknownCommand(t *testing.T) {
	port, _ := startTestDaemon(t)

	sessionID := "test-session-2"
	tools := []HostTool{
		{Name: "deploy", Cmd: "echo deploy"},
	}
	if err := RegisterHostToolSession(port, sessionID, tools, t.TempDir()); err != nil {
		t.Fatalf("register: %v", err)
	}

	resp := sendExecute(t, port, sessionID, "bogus")
	if resp.ExitCode != 1 {
		t.Errorf("exit_code = %d, want 1", resp.ExitCode)
	}
	if !strings.Contains(resp.Output, "unknown command") {
		t.Errorf("output = %q, want to contain 'unknown command'", resp.Output)
	}
	if !strings.Contains(resp.Output, "deploy") {
		t.Errorf("output = %q, want to list available commands", resp.Output)
	}
}

func TestDaemonRejectUnknownSession(t *testing.T) {
	port, _ := startTestDaemon(t)

	resp := sendExecute(t, port, "nonexistent", "hello")
	if resp.ExitCode != 1 {
		t.Errorf("exit_code = %d, want 1", resp.ExitCode)
	}
	if !strings.Contains(resp.Output, "unknown session") {
		t.Errorf("output = %q, want to contain 'unknown session'", resp.Output)
	}
}

func TestDaemonNonzeroExitCode(t *testing.T) {
	port, _ := startTestDaemon(t)

	sessionID := "test-session-exit"
	tools := []HostTool{
		{Name: "fail", Cmd: "exit 42"},
	}
	if err := RegisterHostToolSession(port, sessionID, tools, t.TempDir()); err != nil {
		t.Fatalf("register: %v", err)
	}

	resp := sendExecute(t, port, sessionID, "fail")
	if resp.ExitCode != 42 {
		t.Errorf("exit_code = %d, want 42", resp.ExitCode)
	}
}

func TestDaemonMultipleSessions(t *testing.T) {
	port, _ := startTestDaemon(t)

	// Register two sessions with different commands for the same name.
	if err := RegisterHostToolSession(port, "s1", []HostTool{
		{Name: "greet", Cmd: "echo from-session-1"},
	}, t.TempDir()); err != nil {
		t.Fatalf("register s1: %v", err)
	}
	if err := RegisterHostToolSession(port, "s2", []HostTool{
		{Name: "greet", Cmd: "echo from-session-2"},
	}, t.TempDir()); err != nil {
		t.Fatalf("register s2: %v", err)
	}

	r1 := sendExecute(t, port, "s1", "greet")
	r2 := sendExecute(t, port, "s2", "greet")

	if !strings.Contains(r1.Output, "from-session-1") {
		t.Errorf("s1 output = %q, want from-session-1", r1.Output)
	}
	if !strings.Contains(r2.Output, "from-session-2") {
		t.Errorf("s2 output = %q, want from-session-2", r2.Output)
	}
}

func TestDaemonUnregister(t *testing.T) {
	port, _ := startTestDaemon(t)

	sessionID := "test-unregister"
	if err := RegisterHostToolSession(port, sessionID, []HostTool{
		{Name: "hello", Cmd: "echo hi"},
	}, t.TempDir()); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Should work before unregister.
	resp := sendExecute(t, port, sessionID, "hello")
	if resp.ExitCode != 0 {
		t.Fatalf("execute before unregister failed: %d", resp.ExitCode)
	}

	UnregisterHostToolSession(port, sessionID)

	// Should fail after unregister.
	resp = sendExecute(t, port, sessionID, "hello")
	if resp.ExitCode != 1 {
		t.Errorf("execute after unregister should fail, got exit_code %d", resp.ExitCode)
	}
}

func TestEnsureHostToolDaemonSkipsWhenRunning(t *testing.T) {
	// Start a daemon directly (EnsureHostToolDaemon forks a subprocess which
	// doesn't work in test binaries). Then verify EnsureHostToolDaemon detects
	// the existing daemon and returns immediately.
	port, _ := startTestDaemon(t)

	// Should detect the existing daemon and succeed without forking.
	if err := EnsureHostToolDaemon(port); err != nil {
		t.Fatalf("EnsureHostToolDaemon with running daemon: %v", err)
	}
}

// sendExecute connects to the daemon and sends an execute request.
func sendExecute(t *testing.T, port int, sessionID, command string) hostToolResponse {
	t.Helper()
	return sendMsg(t, port, hostToolMessage{
		Type:    "execute",
		Session: sessionID,
		Command: command,
	})
}

func sendMsg(t *testing.T, port int, msg hostToolMessage) hostToolResponse {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	data, _ := json.Marshal(msg)
	data = append(data, '\n')
	conn.Write(data)

	var buf []byte
	tmp := make([]byte, 4096)
	for {
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}

	var resp hostToolResponse
	if err := json.Unmarshal(buf, &resp); err != nil {
		t.Fatalf("unmarshal response %q: %v", string(buf), err)
	}
	return resp
}
