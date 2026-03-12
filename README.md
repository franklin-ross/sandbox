# Sandbox

A CLI tool for running Claude Code in sandboxed Docker containers with network firewalling and no permission prompts.

## Why

The official Claude Code Docker sandbox has an opinionated auth flow that makes autonomous agent use painful. This tool gives you:

- **Network firewalling** — restricts outbound traffic to Claude API, package registries (npm, Go, Rust, Ruby, PyPI), and GitHub
- **No permission prompts** — uses `--dangerously-skip-permissions` by default, because the container IS the sandbox
- **zsh shell at workspace root** — drops you into zsh, not straight into Claude
- **VSCode attachment** — `sandbox code .` opens VSCode remote into the container
- **Auto-sync files** — automatically syncs files in `.sandbox/user/` to the container user directory, including binaries
- **Configuration** — More/simpler configuration options
- **Post-sync hooks** — runs commands inside the container after every sync (e.g., `npm install -g some-tool`)

## Install

Download the binary for your architecture from the releases page, rename it to `sandbox`, and put it somewhere on your $PATH.

Or clone the repo and build from source:

```bash
# Install sandbox binary to ~/bin
task install
```

Requires Docker to be running.

## Usage

On first launch, the tool builds the Docker image automatically, which may take some time.

Claude credentials live inside the sandbox, so you need to log in once for each sandbox.

```bash
# Global initialisation (run once)
sandbox config init

# Open a shell in a running sandbox
sandbox shell ~/projects/myapp

# Open Claude in a directory (with --dangerously-skip-permissions)
sandbox claude project/
# Pass args through to Claude
sandbox claude . -- -p "fix the failing tests"

# Open VSCode connected into to the sandbox
sandbox code .

# List running sandboxes
sandbox ls
# Stop a running sandbox
sandbox stop .
# Remove a sandbox (stops it first if running)
sandbox rm .
# Forcibly copy files, update firewalls, and run on_sync scripts inside
# the sandbox (Not usually necessary to call directly.)
sandbox sync project/
```

## Parent Sandbox Discovery

When you run a command (e.g. `sandbox claude .`), the tool walks up the directory tree looking for a `.sandbox/` directory. If it finds one in a parent, it uses that parent as the sandbox root — names the container after it, loads its config, and mounts its directory. The command itself still runs at your current directory inside the container.

This is useful for monorepos and git worktrees: put `.sandbox/` in the project root and run `sandbox claude` from any subdirectory or worktree without needing separate sandboxes.

```bash
# Given: /home/user/myproject/.sandbox/config.yaml
cd /home/user/myproject/.worktree/feature-branch
sandbox claude .
# → Uses sandbox from /home/user/myproject, runs claude in the worktree dir
```

The tool never treats the user-level `~/.sandbox/` as a parent sandbox (it holds global config only).

Use `--here` to skip parent discovery and force a sandbox at the exact path:

```bash
sandbox --here claude .
```

Destructive commands (`stop`, `rm`) refuse to operate from a child directory to prevent accidents — run them from the sandbox root instead.

## Configuration

Config lives in two places, which the tool merges at load time:

- **Global**: `~/.sandbox/` — applies to all sandboxes
- **Per-workspace**: `<workspace>/.sandbox/` — overrides/extends global

By convention, the tool syncs `.sandbox/home/**/*` into the sandbox. The agent user can execute any Linux binaries in `.sandbox/home/bin/` inside the sandbox.

`.sandbox/config.yaml` provides fine-grained configuration for all containers, or for workspace specific containers.

```yaml
# Copy file globs into the container
sync:
    - src: ~/.oh-my-zsh/custom/themes/*.zsh-theme
      dest: ~/.oh-my-zsh/custom/themes/

env:
    NODE_ENV: development
    GITHUB_TOKEN: $GITHUB_TOKEN # expanded from host env

firewall:
    allow:
        - domain: api.example.com
        - cidr: 10.0.0.0/8

# Run shell commands whenever the config or any sync'd files change
on_sync:
    - name: install deps
      cmd: npm install -g my-tool
    - cmd: chmod 600 ~/.ssh/*
      root: true
```

Whenever this config or any of the synced files change, the next command resynchronises everything into the sandbox.

See [specs/sandbox-config.spec.md](specs/sandbox-config.spec.md) for full details.

### Host Tools

Host tools let the agent inside the sandbox trigger a limited set of pre-configured commands on the host machine. The agent can only send a tool name for now, no arguments to keep things simple.

When you use `sandbox claude`, the tool automatically exposes host tools as MCP tools so Claude sees them as first-class tool calls.

```yaml
host_tools:
    - name: deploy
      description: Deploy the app to staging
      cmd: ./deploy.sh
    - name: restart-db
      description: Restart the PostgreSQL database
      cmd: systemctl restart postgres

# Optional: override the default daemon port (9847)
# host_tool_port: 9848
```

A background daemon on the host manages command execution. It starts automatically on the first `sandbox shell` or `sandbox claude` session and shuts down when the last session ends. Each session registers its workspace's tools, so different workspaces can define different tools under the same name.

The tool automatically configures the firewall to allow the container to reach the daemon.

The daemon logs to `~/.sandbox/daemon/daemon.log` on the host.

## What's in the Container

- Debian Bookworm
- zsh + Oh My Zsh
- Node.js 22 + Yarn
- Go 1.23
- Rust (via rustup)
- Ruby
- Python 3
- Claude Code CLI
- Chromium (for Karma / Playwright / Cypress)
- ripgrep, jq, fzf, tmux, git

## Network Allow List

By default, the firewall allows outbound traffic to:

| Service    | Domains                                                                                                   |
| ---------- | --------------------------------------------------------------------------------------------------------- |
| Claude API | api.anthropic.com, claude.ai, statsig.anthropic.com, sentry.io                                            |
| npm / Yarn | registry.npmjs.org, registry.yarnpkg.com, repo.yarnpkg.com, registry.npmmirror.com                        |
| Go         | proxy.golang.org, sum.golang.org, storage.googleapis.com                                                  |
| Rust       | crates.io, static.crates.io, index.crates.io, static.rust-lang.org                                        |
| Ruby       | rubygems.org, api.rubygems.org, index.rubygems.org                                                        |
| PyPI       | pypi.org, files.pythonhosted.org                                                                          |
| GitHub     | github.com (SSH+HTTP), api.github.com, raw/objects/codeload/pkg-containers.githubusercontent.com, ghcr.io |
| CDNs       | cdn.jsdelivr.net, dl-cdn.alpinelinux.org, deb.nodesource.com                                              |
| Cypress    | download.cypress.io, cdn.cypress.io                                                                       |
| Playwright | cdn.playwright.dev, playwright.download.prss.microsoft.com                                                |

The firewall blocks everything else. It allows DNS so processes inside the container can still resolve hostnames.

## How it Works

The `sandbox` binary embeds the Docker image files via `go:embed`. When you run `sandbox start`, it:

1. Writes the embedded Dockerfile and scripts to a temp directory
2. Builds the image (if not already built)
3. Runs the container with `--cap-add=NET_ADMIN` (for iptables)
4. Mounts your workspace into the container
5. Sets up iptables firewall rules via the entrypoint, then sleeps
