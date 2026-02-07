# sandbox

A CLI tool for running Claude Code in sandboxed Docker containers with network firewalling, persistent SSO auth, and no permission prompts.

## Why

The official Claude Code Docker sandbox has an opinionated auth flow that makes autonomous agent use painful. This tool gives you:

- **SSO auth once, persisted forever** via a shared Docker volume — no re-authenticating across containers
- **Network firewalling** — outbound traffic restricted to Claude API, package registries (npm, Go, Rust, Ruby, PyPI), and GitHub
- **No permission prompts** — `--dangerously-skip-permissions` by default, because the container IS the sandbox
- **zsh shell at workspace root** — not dropped straight into Claude
- **VSCode attachment** — `sandbox code .` opens VSCode remote into the container

## Install

```bash
# From the repo root
task build:sandbox

# Or install to $GOPATH/bin
task install:sandbox
```

Requires Docker to be running.

## Usage

```bash
# Start a sandbox and open a shell
sandbox .

# Start without attaching
sandbox start .
sandbox start ~/projects/myapp

# Open a shell in a running sandbox
sandbox shell ~/projects/myapp

# Open Claude (with --dangerously-skip-permissions)
sandbox claude .

# Pass args through to Claude
sandbox claude . -- -p "fix the failing tests"

# Open VSCode attached to the sandbox
sandbox code .

# List running sandboxes
sandbox ls

# Stop and remove a sandbox
sandbox stop .

# Force rebuild the Docker image
sandbox build
```

## First run

On first launch, the Docker image will be built automatically. This takes a few minutes (Ubuntu + Node, Go, Rust, Ruby, Python).

You'll need to authenticate Claude once:

```bash
sandbox .
# Inside the container:
claude /login
```

This stores credentials in a Docker volume (`ao-sandbox-claude-creds`) shared across all sandboxes. You won't need to log in again.

## What's in the container

- Ubuntu 24.04
- zsh + Oh My Zsh
- Node.js 22 + Yarn
- Go 1.23
- Rust (via rustup)
- Ruby 3.3
- Python 3
- Claude Code CLI
- ripgrep, jq, fzf, tmux, git

## Network whitelist

The firewall allows outbound traffic to:

| Service    | Domains                                                  |
| ---------- | -------------------------------------------------------- |
| Claude API | api.anthropic.com, api.claude.ai, claude.ai              |
| npm / Yarn | registry.npmjs.org, registry.yarnpkg.com                 |
| Go         | proxy.golang.org, sum.golang.org, storage.googleapis.com |
| Rust       | crates.io, static.crates.io, static.rust-lang.org        |
| Ruby       | rubygems.org                                             |
| PyPI       | pypi.org, files.pythonhosted.org                         |
| GitHub     | github.com, api.github.com, \*.githubusercontent.com     |

Everything else is blocked. DNS is allowed so domains can be resolved at container start time.

## How it works

The Docker image files are embedded in the `sandbox` binary via `go:embed`. When you run `sandbox start`, it:

1. Writes the embedded Dockerfile and scripts to a temp directory
2. Builds the image (if not already built)
3. Runs the container with `--cap-add=NET_ADMIN` (for iptables)
4. Mounts your workspace at `/workspace` and the credentials volume at `~/.claude`
5. The entrypoint sets up iptables firewall rules, then sleeps

You then interact via `sandbox shell`, `sandbox claude`, or `sandbox code`.
