package hosttool

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

const testToken = "test-token-abc123"

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
		// Tiny race window: bind the port in RunDaemon.
		// Signal ready once we know it's started (or errored).
		close(ready)
		RunDaemon(ctx, port)
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
	tools := []Tool{
		{Name: "hello", Cmd: "echo hello-world"},
	}
	if err := RegisterSession(port, sessionID, testToken, tools, t.TempDir()); err != nil {
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
	tools := []Tool{
		{Name: "deploy", Cmd: "echo deploy"},
	}
	if err := RegisterSession(port, sessionID, testToken, tools, t.TempDir()); err != nil {
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
	tools := []Tool{
		{Name: "fail", Cmd: "exit 42"},
	}
	if err := RegisterSession(port, sessionID, testToken, tools, t.TempDir()); err != nil {
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
	if err := RegisterSession(port, "s1", testToken, []Tool{
		{Name: "greet", Cmd: "echo from-session-1"},
	}, t.TempDir()); err != nil {
		t.Fatalf("register s1: %v", err)
	}
	if err := RegisterSession(port, "s2", testToken, []Tool{
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
	if err := RegisterSession(port, sessionID, testToken, []Tool{
		{Name: "hello", Cmd: "echo hi"},
	}, t.TempDir()); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Should work before unregister.
	resp := sendExecute(t, port, sessionID, "hello")
	if resp.ExitCode != 0 {
		t.Fatalf("execute before unregister failed: %d", resp.ExitCode)
	}

	UnregisterSession(port, sessionID)

	// Should fail after unregister.
	resp = sendExecute(t, port, sessionID, "hello")
	if resp.ExitCode != 1 {
		t.Errorf("execute after unregister should fail, got exit_code %d", resp.ExitCode)
	}
}

func TestEnsureDaemonSkipsWhenRunning(t *testing.T) {
	// Start a daemon directly (EnsureDaemon forks a subprocess which
	// doesn't work in test binaries). Then verify EnsureDaemon detects
	// the existing daemon and returns immediately.
	port, _ := startTestDaemon(t)

	// Should detect the existing daemon and succeed without forking.
	if err := EnsureDaemon(port); err != nil {
		t.Fatalf("EnsureDaemon with running daemon: %v", err)
	}
}

func TestDaemonExecuteWithArgs(t *testing.T) {
	port, _ := startTestDaemon(t)
	sessionID := "test-args-1"
	tools := []Tool{{
		Name: "greet",
		Cmd:  "echo ${name}",
		Args: []Arg{{Name: "name"}},
	}}
	if err := RegisterSession(port, sessionID, testToken, tools, t.TempDir()); err != nil {
		t.Fatalf("register: %v", err)
	}

	resp := sendMsg(t, port, message{
		Type:    "execute",
		Session: sessionID,
		Token:   testToken,
		Command: "greet",
		Args:    map[string]any{"name": "world"},
	})
	if resp.ExitCode != 0 {
		t.Fatalf("exit = %d, out = %q", resp.ExitCode, resp.Output)
	}
	if !strings.Contains(resp.Output, "world") {
		t.Errorf("output = %q", resp.Output)
	}
}

func TestDaemonExecuteArgValidationFails(t *testing.T) {
	port, _ := startTestDaemon(t)
	sessionID := "test-args-2"
	tools := []Tool{{
		Name: "scale",
		Cmd:  "echo ${n}",
		Args: []Arg{{
			Name: "n", Type: "integer", Min: ptrFloat(0), Max: ptrFloat(10),
		}},
	}}
	if err := RegisterSession(port, sessionID, testToken, tools, t.TempDir()); err != nil {
		t.Fatalf("register: %v", err)
	}

	resp := sendMsg(t, port, message{
		Type:    "execute",
		Session: sessionID,
		Token:   testToken,
		Command: "scale",
		Args:    map[string]any{"n": float64(999)},
	})
	if resp.ExitCode != 2 {
		t.Errorf("exit = %d, want 2", resp.ExitCode)
	}
	if !strings.Contains(resp.Output, "max") {
		t.Errorf("output = %q, want mention of max", resp.Output)
	}
}

func TestDaemonExecuteShellQuoting(t *testing.T) {
	port, _ := startTestDaemon(t)
	sessionID := "test-args-3"
	tools := []Tool{{
		Name: "echo",
		Cmd:  "echo ${msg}",
		Args: []Arg{{Name: "msg"}},
	}}
	if err := RegisterSession(port, sessionID, testToken, tools, t.TempDir()); err != nil {
		t.Fatalf("register: %v", err)
	}

	resp := sendMsg(t, port, message{
		Type:    "execute",
		Session: sessionID,
		Token:   testToken,
		Command: "echo",
		Args:    map[string]any{"msg": "hi; echo PWNED"},
	})
	if resp.ExitCode != 0 {
		t.Fatalf("exit = %d, out = %q", resp.ExitCode, resp.Output)
	}
	// If quoting worked, output is a single line "hi; echo PWNED".
	// If injection succeeded, output would be two lines "hi" + "PWNED".
	if strings.TrimRight(resp.Output, "\n") != "hi; echo PWNED" {
		t.Errorf("injection or quoting broken: output = %q", resp.Output)
	}
}

func TestDaemonRejectExecuteBadToken(t *testing.T) {
	port, _ := startTestDaemon(t)

	sessionID := "test-token-mismatch"
	tools := []Tool{{Name: "hello", Cmd: "echo hi"}}
	if err := RegisterSession(port, sessionID, testToken, tools, t.TempDir()); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Execute with the wrong token must be rejected before the command runs.
	resp := sendMsg(t, port, message{
		Type:    "execute",
		Session: sessionID,
		Token:   "wrong-token",
		Command: "hello",
	})
	if resp.ExitCode != 1 {
		t.Errorf("exit_code = %d, want 1", resp.ExitCode)
	}
	if !strings.Contains(resp.Output, "unauthorized") {
		t.Errorf("output = %q, want 'unauthorized'", resp.Output)
	}

	// An empty token (as a no-token client would send) is also rejected.
	resp = sendMsg(t, port, message{Type: "execute", Session: sessionID, Command: "hello"})
	if resp.ExitCode != 1 || !strings.Contains(resp.Output, "unauthorized") {
		t.Errorf("no-token execute: exit=%d output=%q, want unauthorized", resp.ExitCode, resp.Output)
	}
}

// TestDataPlaneRejectsRegister models the sandbox container: the only channel
// it can reach is the TCP data plane (the runtime makes its connection look
// like loopback). register/unregister must be refused there regardless of any
// token, so a prompt-injected agent cannot define its own tool.
func TestDataPlaneRejectsRegister(t *testing.T) {
	port, _ := startTestDaemon(t)

	resp := sendMsg(t, port, message{
		Type:    "register",
		Session: "attacker",
		Control: testToken, // even if it somehow knew a control token
		Token:   "attacker-token",
		Tools:   []Tool{{Name: "evil", Cmd: "echo PWNED"}},
		Workdir: t.TempDir(),
	})
	if resp.OK {
		t.Fatal("register over the data plane was accepted, want rejected")
	}
	if !strings.Contains(resp.Output, "data plane") {
		t.Errorf("output = %q, want mention of data plane", resp.Output)
	}

	// The attacker's tool must not exist / run.
	resp = sendMsg(t, port, message{
		Type:    "execute",
		Session: "attacker",
		Token:   "attacker-token",
		Command: "evil",
	})
	if resp.ExitCode == 0 || strings.Contains(resp.Output, "PWNED") {
		t.Fatalf("attacker tool executed: exit=%d output=%q", resp.ExitCode, resp.Output)
	}
}

// TestControlPlaneRejectsBadControlToken verifies the second layer: even a
// caller that reaches the host-only control socket must present the control
// token to register.
func TestControlPlaneRejectsBadControlToken(t *testing.T) {
	port, _ := startTestDaemon(t)

	for _, control := range []string{"", "guessed-control-token"} {
		resp := sendControlMsg(t, port, message{
			Type:    "register",
			Session: "attacker",
			Control: control,
			Token:   "attacker-token",
			Tools:   []Tool{{Name: "evil", Cmd: "echo PWNED"}},
			Workdir: t.TempDir(),
		})
		if resp.OK {
			t.Fatalf("control-plane register with control=%q was accepted, want rejected", control)
		}
	}
}

// TestControlPlaneRejectsExecute verifies execute is not accepted on the
// control plane (it belongs to the data plane).
func TestControlPlaneRejectsExecute(t *testing.T) {
	port, _ := startTestDaemon(t)
	resp := sendControlMsg(t, port, message{Type: "execute", Session: "x", Command: "y"})
	if resp.OK || !strings.Contains(resp.Output, "control plane") {
		t.Errorf("execute on control plane: ok=%v output=%q, want rejected", resp.OK, resp.Output)
	}
}

// sendExecute connects to the daemon and sends an execute request.
func sendExecute(t *testing.T, port int, sessionID, command string) response {
	t.Helper()
	return sendMsg(t, port, message{
		Type:    "execute",
		Session: sessionID,
		Token:   testToken,
		Command: command,
	})
}

// sendMsg sends a message over the TCP data plane.
func sendMsg(t *testing.T, port int, msg message) response {
	t.Helper()
	return sendOn(t, "tcp", fmt.Sprintf("127.0.0.1:%d", port), msg)
}

// sendControlMsg sends a message over the Unix-socket control plane.
func sendControlMsg(t *testing.T, port int, msg message) response {
	t.Helper()
	return sendOn(t, "unix", controlSocketPath(port), msg)
}

func sendOn(t *testing.T, network, addr string, msg message) response {
	t.Helper()
	conn, err := net.DialTimeout(network, addr, time.Second)
	if err != nil {
		t.Fatalf("dial %s %s: %v", network, addr, err)
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

	var resp response
	if err := json.Unmarshal(buf, &resp); err != nil {
		t.Fatalf("unmarshal response %q: %v", string(buf), err)
	}
	return resp
}
