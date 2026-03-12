package cmd

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// hostcmdPidFile returns the path to the daemon PID file.
func hostcmdPidFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".sandbox", "hostcmd-daemon.pid")
}

// GenerateSessionID returns a random 8-byte hex string.
func GenerateSessionID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// --- Protocol types ---

type hostcmdMessage struct {
	Type     string        `json:"type"`               // "register", "execute", "unregister"
	Session  string        `json:"session"`             // session ID
	Command  string        `json:"command,omitempty"`   // for execute
	Commands []HostCommand `json:"commands,omitempty"`  // for register
	Workdir  string        `json:"workdir,omitempty"`   // for register
}

type hostcmdResponse struct {
	OK       bool   `json:"ok"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

// --- Session registry ---

type sessionEntry struct {
	commands map[string]string // name → cmd
	workdir  string
}

// --- Daemon ---

// HostcmdDaemon listens on a TCP port and executes pre-configured host commands
// dispatched by session ID.
type HostcmdDaemon struct {
	listener net.Listener
	mu       sync.Mutex
	sessions map[string]*sessionEntry
	cancel   context.CancelFunc
	done     chan struct{} // closed when serve() returns
}

// RunHostcmdDaemon creates a TCP listener and serves until the context is
// cancelled or the last session unregisters. This blocks and is intended to
// be the main loop of the daemon process.
func RunHostcmdDaemon(ctx context.Context, port int) error {
	addr := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp4", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	ctx, cancel := context.WithCancel(ctx)
	d := &HostcmdDaemon{
		listener: listener,
		sessions: make(map[string]*sessionEntry),
		cancel:   cancel,
		done:     make(chan struct{}),
	}

	// Write PID file.
	pidFile := hostcmdPidFile()
	os.MkdirAll(filepath.Dir(pidFile), 0755)
	os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
	defer os.Remove(pidFile)

	d.serve(ctx)
	return nil
}

func (d *HostcmdDaemon) serve(ctx context.Context) {
	defer close(d.done)
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			continue
		}
		go d.handleConn(ctx, conn)
	}
}

func (d *HostcmdDaemon) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	// Allow up to 1MB messages (large command output).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !scanner.Scan() {
		return
	}

	var msg hostcmdMessage
	if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
		json.NewEncoder(conn).Encode(hostcmdResponse{ExitCode: 1, Output: "invalid request: " + err.Error()})
		return
	}

	switch msg.Type {
	case "register":
		d.handleRegister(conn, msg)
	case "execute":
		d.handleExecute(ctx, conn, msg)
	case "unregister":
		d.handleUnregister(conn, msg)
	default:
		json.NewEncoder(conn).Encode(hostcmdResponse{ExitCode: 1, Output: "unknown message type: " + msg.Type})
	}
}

func (d *HostcmdDaemon) handleRegister(conn net.Conn, msg hostcmdMessage) {
	cmds := make(map[string]string, len(msg.Commands))
	for _, hc := range msg.Commands {
		cmds[hc.Name] = hc.Cmd
	}

	d.mu.Lock()
	d.sessions[msg.Session] = &sessionEntry{commands: cmds, workdir: msg.Workdir}
	d.mu.Unlock()

	json.NewEncoder(conn).Encode(hostcmdResponse{OK: true})
}

func (d *HostcmdDaemon) handleUnregister(conn net.Conn, msg hostcmdMessage) {
	d.mu.Lock()
	delete(d.sessions, msg.Session)
	remaining := len(d.sessions)
	d.mu.Unlock()

	json.NewEncoder(conn).Encode(hostcmdResponse{OK: true})

	if remaining == 0 {
		// Last session gone — shut down after a grace period so a quick
		// restart doesn't have to re-launch the daemon.
		go func() {
			time.Sleep(5 * time.Second)
			d.mu.Lock()
			n := len(d.sessions)
			d.mu.Unlock()
			if n == 0 {
				d.cancel()
				d.listener.Close()
			}
		}()
	}
}

func (d *HostcmdDaemon) handleExecute(ctx context.Context, conn net.Conn, msg hostcmdMessage) {
	d.mu.Lock()
	sess, ok := d.sessions[msg.Session]
	d.mu.Unlock()

	if !ok {
		json.NewEncoder(conn).Encode(hostcmdResponse{
			ExitCode: 1,
			Output:   fmt.Sprintf("unknown session %q", msg.Session),
		})
		return
	}

	cmdStr, ok := sess.commands[msg.Command]
	if !ok {
		names := make([]string, 0, len(sess.commands))
		for n := range sess.commands {
			names = append(names, n)
		}
		sort.Strings(names)
		json.NewEncoder(conn).Encode(hostcmdResponse{
			ExitCode: 1,
			Output:   fmt.Sprintf("unknown command %q; available: %s", msg.Command, strings.Join(names, ", ")),
		})
		return
	}

	// 5-minute timeout per command.
	execCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "sh", "-c", cmdStr)
	cmd.Dir = sess.workdir
	output, err := cmd.CombinedOutput()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	json.NewEncoder(conn).Encode(hostcmdResponse{ExitCode: exitCode, Output: string(output)})
}

// --- Client helpers (used by sandbox claude / sandbox shell) ---

// EnsureHostcmdDaemon checks if the daemon is running on the given port. If
// not, it starts one as a detached subprocess. Returns nil on success.
func EnsureHostcmdDaemon(port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err == nil {
		conn.Close()
		return nil // already running
	}

	// Start daemon as a detached subprocess.
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}

	cmd := exec.Command(exe, "daemon", "--port", fmt.Sprintf("%d", port))
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	// Detach from parent process group so it outlives us.
	setSysProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start hostcmd daemon: %w", err)
	}
	// Release so we don't wait for it.
	cmd.Process.Release()

	// Wait for daemon to be ready.
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
	}
	return fmt.Errorf("hostcmd daemon did not start within 1s on port %d", port)
}

// RegisterHostcmdSession registers a session's commands with the running daemon.
func RegisterHostcmdSession(port int, sessionID string, commands []HostCommand, workdir string) error {
	return sendHostcmdMessage(port, hostcmdMessage{
		Type:     "register",
		Session:  sessionID,
		Commands: commands,
		Workdir:  workdir,
	})
}

// UnregisterHostcmdSession removes a session from the daemon.
func UnregisterHostcmdSession(port int, sessionID string) {
	// Best-effort; daemon may already be gone.
	sendHostcmdMessage(port, hostcmdMessage{
		Type:    "unregister",
		Session: sessionID,
	})
}

func sendHostcmdMessage(port int, msg hostcmdMessage) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return fmt.Errorf("connect to hostcmd daemon: %w", err)
	}
	defer conn.Close()

	data, _ := json.Marshal(msg)
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("send to hostcmd daemon: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return fmt.Errorf("no response from hostcmd daemon")
	}
	var resp hostcmdResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return fmt.Errorf("invalid response from hostcmd daemon: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("hostcmd daemon error: %s", resp.Output)
	}
	return nil
}
