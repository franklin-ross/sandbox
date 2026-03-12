package cmd

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// hostToolPidFile returns the path to the daemon PID file.
func hostToolPidFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".sandbox", "daemon", "daemon.pid")
}

// hostToolLogFile returns the path to the daemon log file.
func hostToolLogFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".sandbox", "daemon", "daemon.log")
}

// GenerateSessionID returns a random 8-byte hex string.
func GenerateSessionID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// --- Protocol types ---

type hostToolMessage struct {
	Type    string     `json:"type"`              // "register", "execute", "unregister"
	Session string     `json:"session"`           // session ID
	Command string     `json:"command,omitempty"` // for execute
	Tools   []HostTool `json:"tools,omitempty"`   // for register
	Workdir string     `json:"workdir,omitempty"` // for register
}

type hostToolResponse struct {
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

// HostToolDaemon listens on a TCP port and executes pre-configured host tools
// dispatched by session ID.
type HostToolDaemon struct {
	listener net.Listener
	mu       sync.Mutex
	sessions map[string]*sessionEntry
	cancel   context.CancelFunc
	done     chan struct{} // closed when serve() returns
	log      *log.Logger
}

// RunHostToolDaemon creates a TCP listener and serves until the context is
// cancelled or the last session unregisters. This blocks and is intended to
// be the main loop of the daemon process.
func RunHostToolDaemon(ctx context.Context, port int) error {
	logFile := hostToolLogFile()
	os.MkdirAll(filepath.Dir(logFile), 0755)
	// Truncate if over 1 MB.
	if info, err := os.Stat(logFile); err == nil && info.Size() > 1<<20 {
		os.Truncate(logFile, 0)
	}
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()
	logger := log.New(f, "", log.LstdFlags)

	addr := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp4", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	logger.Printf("daemon started on %s (pid %d)", addr, os.Getpid())

	ctx, cancel := context.WithCancel(ctx)
	d := &HostToolDaemon{
		listener: listener,
		sessions: make(map[string]*sessionEntry),
		cancel:   cancel,
		done:     make(chan struct{}),
		log:      logger,
	}

	// Write PID file with binary mtime so clients can detect stale daemons.
	pidFile := hostToolPidFile()
	os.MkdirAll(filepath.Dir(pidFile), 0755)
	pidData := fmt.Sprintf("%d\n%s", os.Getpid(), binaryMtime())
	os.WriteFile(pidFile, []byte(pidData), 0644)
	defer os.Remove(pidFile)

	d.serve(ctx)
	logger.Println("daemon stopped")
	return nil
}

func (d *HostToolDaemon) serve(ctx context.Context) {
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

func (d *HostToolDaemon) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	// Allow up to 1MB messages (large command output).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !scanner.Scan() {
		return
	}

	var msg hostToolMessage
	if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
		d.log.Printf("invalid request from %s: %v", conn.RemoteAddr(), err)
		json.NewEncoder(conn).Encode(hostToolResponse{ExitCode: 1, Output: "invalid request: " + err.Error()})
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
		d.log.Printf("unknown message type %q from %s", msg.Type, conn.RemoteAddr())
		json.NewEncoder(conn).Encode(hostToolResponse{ExitCode: 1, Output: "unknown message type: " + msg.Type})
	}
}

func (d *HostToolDaemon) handleRegister(conn net.Conn, msg hostToolMessage) {
	cmds := make(map[string]string, len(msg.Tools))
	for _, ht := range msg.Tools {
		cmds[ht.Name] = ht.Cmd
	}

	d.mu.Lock()
	d.sessions[msg.Session] = &sessionEntry{commands: cmds, workdir: msg.Workdir}
	d.mu.Unlock()

	names := make([]string, 0, len(cmds))
	for n := range cmds {
		names = append(names, n)
	}
	sort.Strings(names)
	d.log.Printf("registered session %s with %d tools (%s), workdir=%s",
		msg.Session, len(cmds), strings.Join(names, ", "), msg.Workdir)

	json.NewEncoder(conn).Encode(hostToolResponse{OK: true})
}

func (d *HostToolDaemon) handleUnregister(conn net.Conn, msg hostToolMessage) {
	d.mu.Lock()
	delete(d.sessions, msg.Session)
	remaining := len(d.sessions)
	d.mu.Unlock()

	d.log.Printf("unregistered session %s (%d remaining)", msg.Session, remaining)

	json.NewEncoder(conn).Encode(hostToolResponse{OK: true})

	if remaining == 0 {
		// Last session gone — shut down after a grace period so a quick
		// restart doesn't have to re-launch the daemon.
		go func() {
			time.Sleep(5 * time.Second)
			d.mu.Lock()
			n := len(d.sessions)
			d.mu.Unlock()
			if n == 0 {
				d.log.Println("no sessions remaining after grace period, shutting down")
				d.cancel()
				d.listener.Close()
			}
		}()
	}
}

func (d *HostToolDaemon) handleExecute(ctx context.Context, conn net.Conn, msg hostToolMessage) {
	d.mu.Lock()
	sess, ok := d.sessions[msg.Session]
	d.mu.Unlock()

	if !ok {
		d.log.Printf("execute %q: unknown session %q", msg.Command, msg.Session)
		json.NewEncoder(conn).Encode(hostToolResponse{
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
		d.log.Printf("execute %q: unknown command (session %s has: %s)", msg.Command, msg.Session, strings.Join(names, ", "))
		json.NewEncoder(conn).Encode(hostToolResponse{
			ExitCode: 1,
			Output:   fmt.Sprintf("unknown command %q; available: %s", msg.Command, strings.Join(names, ", ")),
		})
		return
	}

	d.log.Printf("execute %q (session %s): running %q in %s", msg.Command, msg.Session, cmdStr, sess.workdir)

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

	d.log.Printf("execute %q (session %s): exit %d (%d bytes output)", msg.Command, msg.Session, exitCode, len(output))
	json.NewEncoder(conn).Encode(hostToolResponse{ExitCode: exitCode, Output: string(output)})
}

// --- Client helpers (used by sandbox claude / sandbox shell) ---

// binaryMtime returns the modification time of the current binary as a string.
func binaryMtime() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	info, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	return info.ModTime().UTC().Format(time.RFC3339Nano)
}

// EnsureHostToolDaemon checks if the daemon is running on the given port. If
// the daemon was started by a different version of the binary, it kills the
// old one and starts a fresh daemon. Returns nil on success.
func EnsureHostToolDaemon(port int) error {
	// Check for stale daemon from a previous binary version.
	if needsRestart() {
		killStaleDaemon()
	}

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
		return fmt.Errorf("start host tool daemon: %w", err)
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
	return fmt.Errorf("host tool daemon did not start within 1s on port %d", port)
}

// needsRestart returns true if the PID file exists but was written by a
// different version of the binary (detected via mtime).
func needsRestart() bool {
	data, err := os.ReadFile(hostToolPidFile())
	if err != nil {
		return false
	}
	lines := strings.SplitN(string(data), "\n", 2)
	if len(lines) < 2 {
		return true // old format without mtime — assume stale
	}
	return strings.TrimSpace(lines[1]) != binaryMtime()
}

// killStaleDaemon reads the PID file and kills the old daemon process.
func killStaleDaemon() {
	data, err := os.ReadFile(hostToolPidFile())
	if err != nil {
		return
	}
	lines := strings.SplitN(string(data), "\n", 2)
	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	proc.Signal(os.Interrupt)
	// Give it a moment to clean up, then force-kill.
	time.Sleep(200 * time.Millisecond)
	proc.Kill()
	os.Remove(hostToolPidFile())
}

// RegisterHostToolSession registers a session's tools with the running daemon.
func RegisterHostToolSession(port int, sessionID string, tools []HostTool, workdir string) error {
	return sendHostToolMessage(port, hostToolMessage{
		Type:    "register",
		Session: sessionID,
		Tools:   tools,
		Workdir: workdir,
	})
}

// UnregisterHostToolSession removes a session from the daemon.
func UnregisterHostToolSession(port int, sessionID string) {
	// Best-effort; daemon may already be gone.
	sendHostToolMessage(port, hostToolMessage{
		Type:    "unregister",
		Session: sessionID,
	})
}

func sendHostToolMessage(port int, msg hostToolMessage) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return fmt.Errorf("connect to host tool daemon: %w", err)
	}
	defer conn.Close()

	data, _ := json.Marshal(msg)
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("send to host tool daemon: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return fmt.Errorf("no response from host tool daemon")
	}
	var resp hostToolResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return fmt.Errorf("invalid response from host tool daemon: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("host tool daemon error: %s", resp.Output)
	}
	return nil
}
