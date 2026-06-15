# ADR: TCP for host-sandbox communication

## Status

Accepted

## Context

The sandbox runs code inside a Docker container while the host
orchestrates lifecycle, configuration, and — via the `host_commands`
feature — executes a curated set of privileged commands on behalf of the
agent. This requires a reliable IPC channel between a client inside the
container and a daemon on the host.

Docker on macOS does not run containers natively. It runs them inside a
Linux VM managed by OrbStack, Docker Desktop, Colima, or similar. The
macOS host and the Linux container therefore operate under **separate
kernels**. While the virtualised filesystem layer (virtiofs / 9p) can
share regular files, kernel-level IPC objects cannot cross the boundary.

## Options considered

### 1. Unix sockets (rejected)

We created a Unix socket on the host at
`/tmp/sandbox-hostcmd-<name>/host.sock` and bind-mounted the directory
into the container at `/run/sandbox-hostcmd/`.

The container could see the socket file (`ls` showed `srw-rw-rw-`), but
`connect()` returned **ECONNREFUSED**. Unix sockets are kernel objects —
the file descriptor, signalling, and blocking semantics live in the
macOS kernel and the Linux kernel in the VM cannot reach them. virtiofs
exposes the directory entry but not the underlying socket.

### 2. FIFO named pipes (rejected)

We tested FIFOs as a simpler alternative. We created a pipe on the host
with `mkfifo`, mounted it into an Alpine container, and attempted a
cross-boundary read/write.

The container read timed out (exit 143 / SIGTERM). FIFOs have the same
fundamental problem: the kernel implements the blocking-open semantics
(writer blocks until reader opens, and vice versa). With writer and
reader in different kernels, the notification never arrives.

### 3. File polling (considered, not chosen)

We considered a design where the client writes `<id>.req`, the daemon
polls for request files (~100 ms interval), executes the command, and
writes `<id>.resp`. Regular file I/O works fine over virtiofs.

Pros: no network, no firewall changes, simple implementation.
Cons: polling latency, file cleanup, no natural backpressure. We kept
this as a viable fallback but chose TCP instead.

### 4. TCP via `host.docker.internal` (chosen)

Every Docker runtime provides the special hostname
`host.docker.internal` inside containers, which resolves to a gateway IP
that routes to the host (`0.250.250.254` on OrbStack, `192.168.65.254`
on Docker Desktop, etc.). TCP traffic over this path works because
Docker runtimes explicitly bridge the network stack between the VM and
the host.

## Decision

Use TCP with newline-delimited JSON on a fixed, configurable port
(default **9847**).

The host runs a daemon that listens on `0.0.0.0:<port>` (forced to
IPv4 via Go's `tcp4` network type). A Node.js client script inside the
container connects to `host.docker.internal:<port>`, sends a single JSON
message, reads the response, and disconnects.

### Key design choices

- **Fixed port** — a fixed port keeps the iptables firewall rule
  static. We write the rule once at container sync time; the daemon
  starting or stopping does not require dynamic iptables modifications.
- **`tcp4` listener** — macOS silently creates an IPv6 socket for
  `0.0.0.0` binds. Since `host.docker.internal` resolves to an IPv4
  address, we force an IPv4-only listener to accept connections.
- **Resolve gateway from inside the container** —
  `host.docker.internal` only resolves inside a container, not on the
  macOS host. The firewall rule generator runs
  `docker exec <ctr> getent hosts host.docker.internal` to obtain the
  correct IP, which keeps the approach portable across Docker runtimes.
- **Session-based dispatch** — each `sandbox shell` / `sandbox claude`
  invocation generates a random session ID, registers its allowed
  commands with the daemon, and passes `SANDBOX_SESSION` as an
  environment variable into the container. The daemon looks up the
  session to find the right command set.
- **Detached daemon subprocess** — the first session starts the daemon
  as a background process (via `Setsid`). The daemon shuts itself down
  5 seconds after the last session unregisters.

## Consequences

- Each container needs a narrow firewall exception for
  `host.docker.internal` on the configured port. This adds a single
  iptables rule and does not grant access to any other host service.
- The approach depends on `host.docker.internal` being resolvable, which
  all major Docker runtimes (OrbStack, Docker Desktop, Colima, Rancher
  Desktop) support. A non-standard runtime that lacks this hostname
  would need adaptation.

## Addendum: dual-plane transport for sandbox-escape hardening

A security review found that a single TCP channel let a prompt-injected
agent in the container `register` its own tool (an arbitrary host command)
and then `execute` it — a full sandbox escape. Source-address filtering
does **not** help: the Docker runtime NATs the container's
`host.docker.internal` connection onto the host **loopback**, so the
daemon sees `127.0.0.1` for both the container and the host orchestrator
(verified on OrbStack). Source-address filtering cannot distinguish them.

The protocol is now split into two planes:

- **Data plane — TCP loopback (`127.0.0.1:<port>`).** Serves `execute`
  only. The container reaches it via `host.docker.internal`. We narrowed
  the bind from `0.0.0.0` to loopback to keep it off the LAN. Each
  `execute` must present a per-session token handed to the container via
  its environment.
- **Control plane — Unix domain socket
  (`~/.sandbox/daemon/control-<port>.sock`, mode 0600).** Serves
  `register` / `unregister` only. This reuses the finding from option 1
  above: a container in the VM **cannot `connect()` to a host Unix
  socket** (`ECONNREFUSED`, re-verified — even with the socket
  bind-mounted in). What made Unix sockets unusable for the data plane is
  exactly the property we want for the control plane: only the host
  orchestrator, sharing the daemon's kernel, can reach it. A host-only
  control token is still required as a second layer.

The data plane rejects `register`/`unregister` outright, and the control
plane rejects `execute`, so neither channel can perform the other's
operations.

Defence in depth around this:

- The daemon's control token lives in a 0600 file the container does not
  mount; secrets are never passed as process arguments (`docker exec`
  uses the name-only `-e NAME` env-passthrough form, not `-e NAME=value`,
  so values never appear in `ps`).
- `sandbox` refuses to mount the home directory (or any ancestor) or the
  `~/.sandbox` control directory into a container, so the container cannot
  read the control socket or token that way.
