# sandbox

A CLI tool for running Claude Code in sandboxed Docker containers with network firewalling and no permission prompts.

## Why

The official Claude Code Docker sandbox has an opinionated auth flow that makes autonomous agent use painful. This tool gives you:

- **Network firewalling** — outbound traffic restricted to Claude API, package registries (npm, Go, Rust, Ruby, PyPI), and GitHub
- **No permission prompts** — `--dangerously-skip-permissions` by default, because the container IS the sandbox
- **zsh shell at workspace root** — not dropped straight into Claude
- **VSCode attachment** — `sandbox code .` opens VSCode remote into the container
- **Configuration** — More/simpler configuration options

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

# Open a shell in a running sandbox in the current directory
sandbox shell ~/projects/myapp

# Open Claude in a directory (with --dangerously-skip-permissions)
sandbox claude project/
# Pass args through to Claude
sandbox claude . -- -p "fix the failing tests"

# Open VSCode attached to the sandbox
sandbox code .

# List running sandboxes
sandbox ls

# Stop a running sandbox
sandbox stop .
# Remove a sandbox (stops it first if running)
sandbox rm .

# Forcibly copy files and update firewalls inside the sandbox
# Other commands do this by default, so not usually necessary
sandbox sync project/
```

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

The firewall allows outbound traffic to:

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
