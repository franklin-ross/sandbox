# sandbox

A CLI tool for running Claude Code in sandboxed Docker containers with network firewalling and no permission prompts.

## Why

The official Claude Code Docker sandbox has an opinionated auth flow that makes autonomous agent use painful. This tool gives you:

- **Network firewalling** — outbound traffic restricted to Claude API, package registries (npm, Go, Rust, Ruby, PyPI), and GitHub
- **No permission prompts** — `--dangerously-skip-permissions` by default, because the container IS the sandbox
- **zsh shell at workspace root** — not dropped straight into Claude
- **VSCode attachment** — `sandbox code .` opens VSCode remote into the container
- **Configuration** — More/simpler configuration options
- **Post-sync hooks** — run commands inside the container after every sync (e.g., `npm install -g some-tool`)

## Install

```bash
# Install sandbox binary to ~/bin
task install
```

Requires Docker to be running.

## Usage

On first launch, the Docker image will build automatically which may take some time.

Claude credentials live inside the sandbox, so you'll need to log in once for each sandbox.

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

## Configuration

Config lives in two places, merged at load time:

- **Global**: `~/.sandbox/` — applies to all sandboxes
- **Per-workspace**: `<workspace>/.sandbox/` — overrides/extends global

By convention, `.sandbox/home/**/*` is sync'd into the sandbox. Any linux binaries in `.sandbox/home/bin/` will be executable by the agent user inside the sandbox.

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
    - cmd: npm install -g my-tool
      name: install deps
    - cmd: chmod 600 ~/.ssh/*
      root: true
```

Whenever this config or any of the synced files change, the next command resynchronises it into the sandbox.

See [specs/sandbox-config.spec.md](specs/sandbox-config.spec.md) for full details.

## What's in the container

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

## Network allowlist

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

Everything else is blocked. DNS is allowed so domains can be resolved at container start time.

## How it works

The Docker image files are embedded in the `sandbox` binary via `go:embed`. When you run `sandbox start`, it:

1. Writes the embedded Dockerfile and scripts to a temp directory
2. Builds the image (if not already built)
3. Runs the container with `--cap-add=NET_ADMIN` (for iptables)
4. Mounts your workspace into the container
5. The entrypoint sets up iptables firewall rules, then sleeps
