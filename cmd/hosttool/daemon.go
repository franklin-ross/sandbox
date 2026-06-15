package hosttool

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
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

// DefaultPort is the default TCP port for the host tool daemon.
const DefaultPort = 9847

// pidFile returns the path to the daemon PID file.
func pidFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".sandbox", "daemon", "daemon.pid")
}

// logFile returns the path to the daemon log file.
func logFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".sandbox", "daemon", "daemon.log")
}

// GenerateSessionID returns a random 8-byte hex string.
func GenerateSessionID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// GenerateToken returns a random 16-byte hex string used to authenticate a
// session's execute requests. The host generates it, registers it with the
// daemon, and hands it to the container; only a client holding the token can
// run that session's tools.
func GenerateToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// controlTokenFile returns the path holding the daemon's control token for a
// given port. The control token authenticates control-plane operations
// (register/unregister). It lives in the host home directory at mode 0600, a
// path the sandbox container does not mount, so an injected agent inside the
// container cannot read it. The filename is keyed by port so concurrent
// daemons (e.g. in tests) don't clobber one another's token.
func controlTokenFile(port int) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".sandbox", "daemon", fmt.Sprintf("control-%d", port))
}

// readControlToken reads the control token for a running daemon on the given
// port. Only callers that can read the host home directory (i.e. the host
// orchestrator, not the container) can obtain it.
func readControlToken(port int) (string, error) {
	data, err := os.ReadFile(controlTokenFile(port))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// controlSocketPath returns the Unix-domain-socket path for the control plane
// (register/unregister). The control plane is deliberately NOT on the network:
// a Docker container runs in a separate VM kernel and cannot connect() to a
// host Unix socket (proven in the project ADR), whereas the host orchestrator
// shares the daemon's kernel and can. The data plane (execute) stays on TCP so
// the container can still reach it via host.docker.internal. The path is keyed
// by port so concurrent daemons (e.g. in tests) don't collide.
func controlSocketPath(port int) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".sandbox", "daemon", fmt.Sprintf("control-%d.sock", port))
}

// --- Protocol types ---

type message struct {
	Type    string         `json:"type"`              // "register", "execute", "unregister"
	Session string         `json:"session"`           // session ID
	Control string         `json:"control,omitempty"` // host-only secret; required for register/unregister
	Token   string         `json:"token,omitempty"`   // per-session secret; required for execute
	Command string         `json:"command,omitempty"` // for execute
	Args    map[string]any `json:"args,omitempty"`    // for execute
	Tools   []Tool         `json:"tools,omitempty"`   // for register
	Workdir string         `json:"workdir,omitempty"` // for register
}

type response struct {
	OK       bool   `json:"ok"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

// --- Session registry ---

type sessionEntry struct {
	tools   map[string]Tool // name → tool
	workdir string
	token   string // secret an execute request must present
}

// --- Daemon ---

// Daemon listens on a TCP port and executes pre-configured host tools
// dispatched by session ID.
type Daemon struct {
	dataListener    net.Listener // TCP loopback; serves execute (container-reachable)
	controlListener net.Listener // Unix socket; serves register/unregister (host-only)
	mu              sync.Mutex
	sessions        map[string]*sessionEntry
	cancel          context.CancelFunc
	done            chan struct{} // closed when both serve loops return
	log             *log.Logger
	control         string // host-only secret required to register/unregister
}

// shutdown cancels the daemon and closes both listeners. Safe to call more than
// once.
func (d *Daemon) shutdown() {
	d.cancel()
	if d.controlListener != nil {
		d.controlListener.Close()
	}
	if d.dataListener != nil {
		d.dataListener.Close()
	}
}

// RunDaemon creates a TCP listener and serves until the context is
// cancelled or the last session unregisters. This blocks and is intended to
// be the main loop of the daemon process.
func RunDaemon(ctx context.Context, port int) error {
	lf := logFile()
	os.MkdirAll(filepath.Dir(lf), 0755)
	// Truncate if over 1 MB.
	if info, err := os.Stat(lf); err == nil && info.Size() > 1<<20 {
		os.Truncate(lf, 0)
	}
	f, err := os.OpenFile(lf, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()
	logger := log.New(f, "", log.LstdFlags)

	// Generate the control token and persist it (mode 0600) before the listener
	// accepts connections, so the orchestrator can read it as soon as the
	// daemon is reachable. The container does not mount this path and therefore
	// cannot read the token — it is what stops the container from registering
	// its own tools (source address can't be trusted: the Docker runtime NATs
	// container connections onto loopback).
	control := GenerateToken()
	ctf := controlTokenFile(port)
	os.MkdirAll(filepath.Dir(ctf), 0755)
	if err := os.WriteFile(ctf, []byte(control), 0600); err != nil {
		return fmt.Errorf("write control token: %w", err)
	}
	defer os.Remove(ctf)

	// Control plane: a Unix socket reachable only from the host (the container's
	// VM kernel cannot connect to it). Bind it before the data plane so it is
	// ready by the time the data port becomes connectable.
	sockPath := controlSocketPath(port)
	os.MkdirAll(filepath.Dir(sockPath), 0755)
	os.Remove(sockPath) // clear any stale socket from a previous daemon
	controlListener, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", sockPath, err)
	}
	// Restrict the socket to the owner so no other local user can drive the
	// control plane even before the control-token check.
	os.Chmod(sockPath, 0600)
	defer os.Remove(sockPath)

	// Data plane: loopback TCP only. The container reaches it via
	// host.docker.internal, which Docker Desktop and OrbStack forward to the
	// host loopback, so this stays reachable while blocking LAN connections
	// outright.
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	dataListener, err := net.Listen("tcp", addr)
	if err != nil {
		controlListener.Close()
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	logger.Printf("daemon started: data=%s control=%s (pid %d)", addr, sockPath, os.Getpid())

	ctx, cancel := context.WithCancel(ctx)
	d := &Daemon{
		dataListener:    dataListener,
		controlListener: controlListener,
		sessions:        make(map[string]*sessionEntry),
		cancel:          cancel,
		done:            make(chan struct{}),
		log:             logger,
		control:         control,
	}

	// Write PID file with binary mtime so clients can detect stale daemons.
	pf := pidFile()
	os.MkdirAll(filepath.Dir(pf), 0755)
	pidData := fmt.Sprintf("%d\n%s", os.Getpid(), binaryMtime())
	os.WriteFile(pf, []byte(pidData), 0644)
	defer os.Remove(pf)

	d.serve(ctx)
	logger.Println("daemon stopped")
	return nil
}

// serve accepts on both planes concurrently and returns when the context is
// cancelled (which closes both listeners).
func (d *Daemon) serve(ctx context.Context) {
	defer close(d.done)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); d.acceptLoop(ctx, d.controlListener, true) }()
	go func() { defer wg.Done(); d.acceptLoop(ctx, d.dataListener, false) }()
	wg.Wait()
}

func (d *Daemon) acceptLoop(ctx context.Context, l net.Listener, control bool) {
	for {
		conn, err := l.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			continue
		}
		go d.handleConn(ctx, conn, control)
	}
}

// handleConn dispatches a single request. The plane determines which message
// types are permitted: register/unregister only on the host-only control
// plane, execute only on the container-reachable data plane. This separation
// is structural — the container cannot reach the control socket at all — and
// the per-message token checks remain as a second layer.
func (d *Daemon) handleConn(ctx context.Context, conn net.Conn, control bool) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	// Allow up to 1MB messages (large command output).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !scanner.Scan() {
		return
	}

	var msg message
	if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
		d.log.Printf("invalid request from %s: %v", conn.RemoteAddr(), err)
		json.NewEncoder(conn).Encode(response{ExitCode: 1, Output: "invalid request: " + err.Error()})
		return
	}

	if control {
		switch msg.Type {
		case "register":
			d.handleRegister(conn, msg)
		case "unregister":
			d.handleUnregister(conn, msg)
		default:
			d.log.Printf("control plane: rejected message type %q", msg.Type)
			json.NewEncoder(conn).Encode(response{ExitCode: 1, Output: "not permitted on control plane: " + msg.Type})
		}
		return
	}

	switch msg.Type {
	case "execute":
		d.handleExecute(ctx, conn, msg)
	default:
		// register/unregister are control-plane only and never accepted here —
		// this is the channel the sandbox container can reach.
		d.log.Printf("data plane: rejected message type %q", msg.Type)
		json.NewEncoder(conn).Encode(response{ExitCode: 1, Output: "not permitted on data plane: " + msg.Type})
	}
}

func (d *Daemon) handleRegister(conn net.Conn, msg message) {
	// Only a caller holding the host-only control token may define what runs on
	// the host. The container cannot read the token (it is in an unmounted host
	// path), so this blocks a prompt-injected agent from registering its own
	// tools — even though the runtime makes its connection appear as loopback.
	if subtle.ConstantTimeCompare([]byte(msg.Control), []byte(d.control)) != 1 {
		d.log.Printf("rejected register with bad control token from %s", conn.RemoteAddr())
		json.NewEncoder(conn).Encode(response{ExitCode: 1, Output: "unauthorized"})
		return
	}

	tools := make(map[string]Tool, len(msg.Tools))
	for _, ht := range msg.Tools {
		tools[ht.Name] = ht
	}

	d.mu.Lock()
	d.sessions[msg.Session] = &sessionEntry{tools: tools, workdir: msg.Workdir, token: msg.Token}
	d.mu.Unlock()

	names := make([]string, 0, len(tools))
	for n := range tools {
		names = append(names, n)
	}
	sort.Strings(names)
	d.log.Printf("registered session %s with %d tools (%s), workdir=%s",
		msg.Session, len(tools), strings.Join(names, ", "), msg.Workdir)

	json.NewEncoder(conn).Encode(response{OK: true})
}

func (d *Daemon) handleUnregister(conn net.Conn, msg message) {
	if subtle.ConstantTimeCompare([]byte(msg.Control), []byte(d.control)) != 1 {
		d.log.Printf("rejected unregister with bad control token from %s", conn.RemoteAddr())
		json.NewEncoder(conn).Encode(response{ExitCode: 1, Output: "unauthorized"})
		return
	}

	d.mu.Lock()
	delete(d.sessions, msg.Session)
	remaining := len(d.sessions)
	d.mu.Unlock()

	d.log.Printf("unregistered session %s (%d remaining)", msg.Session, remaining)

	json.NewEncoder(conn).Encode(response{OK: true})

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
				d.shutdown()
			}
		}()
	}
}

func (d *Daemon) handleExecute(ctx context.Context, conn net.Conn, msg message) {
	d.mu.Lock()
	sess, ok := d.sessions[msg.Session]
	d.mu.Unlock()

	if !ok {
		d.log.Printf("execute %q: unknown session %q", msg.Command, msg.Session)
		json.NewEncoder(conn).Encode(response{
			ExitCode: 1,
			Output:   fmt.Sprintf("unknown session %q", msg.Session),
		})
		return
	}

	// Authenticate the request against the session's secret. This is the gate
	// that stops a LAN attacker (the daemon may bind a routable interface so
	// the container can reach it) from running an existing session's tools.
	if subtle.ConstantTimeCompare([]byte(msg.Token), []byte(sess.token)) != 1 {
		d.log.Printf("execute %q (session %s): token mismatch from %s", msg.Command, msg.Session, conn.RemoteAddr())
		json.NewEncoder(conn).Encode(response{ExitCode: 1, Output: "unauthorized"})
		return
	}

	tool, ok := sess.tools[msg.Command]
	if !ok {
		names := make([]string, 0, len(sess.tools))
		for n := range sess.tools {
			names = append(names, n)
		}
		sort.Strings(names)
		d.log.Printf("execute %q: unknown command (session %s has: %s)", msg.Command, msg.Session, strings.Join(names, ", "))
		json.NewEncoder(conn).Encode(response{
			ExitCode: 1,
			Output:   fmt.Sprintf("unknown command %q; available: %s", msg.Command, strings.Join(names, ", ")),
		})
		return
	}

	argValues, err := ValidateAndCoerceArgs(tool, msg.Args)
	if err != nil {
		d.log.Printf("execute %q (session %s): arg validation failed: %v", msg.Command, msg.Session, err)
		json.NewEncoder(conn).Encode(response{
			ExitCode: 2,
			Output:   "arg validation failed: " + err.Error(),
		})
		return
	}

	cmdStr, err := SubstituteCmd(tool.Cmd, argValues)
	if err != nil {
		d.log.Printf("execute %q (session %s): substitution failed: %v", msg.Command, msg.Session, err)
		json.NewEncoder(conn).Encode(response{
			ExitCode: 2,
			Output:   "substitution failed: " + err.Error(),
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
	json.NewEncoder(conn).Encode(response{ExitCode: exitCode, Output: string(output)})
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

// EnsureDaemon checks if the daemon is running on the given port. If
// the daemon was started by a different version of the binary, it kills the
// old one and starts a fresh daemon. Returns nil on success.
func EnsureDaemon(port int) error {
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
	data, err := os.ReadFile(pidFile())
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
	data, err := os.ReadFile(pidFile())
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
	os.Remove(pidFile())
}

// RegisterSession registers a session's tools with the running daemon. The
// token is the secret an execute request for this session must present. The
// control token (read from the host-only file) authorises the registration
// itself.
func RegisterSession(port int, sessionID, token string, tools []Tool, workdir string) error {
	control, err := readControlToken(port)
	if err != nil {
		return fmt.Errorf("read daemon control token: %w", err)
	}
	return sendControl(port, message{
		Type:    "register",
		Session: sessionID,
		Control: control,
		Token:   token,
		Tools:   tools,
		Workdir: workdir,
	})
}

// UnregisterSession removes a session from the daemon.
func UnregisterSession(port int, sessionID string) {
	// Best-effort; daemon may already be gone.
	control, _ := readControlToken(port)
	sendControl(port, message{
		Type:    "unregister",
		Session: sessionID,
		Control: control,
	})
}

// sendControl dials the daemon's control-plane Unix socket and sends a single
// message. The control plane is host-only; the sandbox container has no route
// to a host Unix socket, so register/unregister are unreachable from it.
func sendControl(port int, msg message) error {
	conn, err := net.DialTimeout("unix", controlSocketPath(port), 2*time.Second)
	if err != nil {
		return fmt.Errorf("connect to host tool daemon control plane: %w", err)
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
	var resp response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return fmt.Errorf("invalid response from host tool daemon: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("host tool daemon error: %s", resp.Output)
	}
	return nil
}
