# Sandbox

A CLI tool for running Claude Code in sandboxed Docker containers with network firewalling and no permission prompts.

## Why

The official Claude Code Docker sandbox has an opinionated auth flow that makes autonomous agent use painful. This tool gives you:

- **Network firewalling** — restricts outbound traffic to Claude API and package registries (npm, Go, Rust, Ruby, PyPI); GitHub is opt-in
- **No permission prompts** — `sandbox claude .` uses `--dangerously-skip-permissions` by default, because the container IS the sandbox
- **Sandboxed VSCode** — `sandbox code .` opens VSCode remote into the container
- **Auto-sync files** — automatically syncs files in `.sandbox/user/` to the container user directory, including binaries
- **Configuration** — More/simpler configuration options
- **Post-sync hooks** — runs commands inside the container after every sync (e.g., `npm install -g some-tool`)
- **Host Tools** — data-driven MCP tools to run privileged commands outside the sandbox without credentials going near the sandbox

## Install

Download the binary for your architecture from the [releases page](https://github.com/franklin-ross/sandbox/releases) and put it on your `$PATH`:

```bash
APPLE_SILICON=sandbox-darwin-arm64
APPLE_INTEL=sandbox-darwin-amd64
LINUX_INTEL=sandbox-linux-amd64
LINUX_ARM=sandbox-linux-arm64

ARCH=$APPLE_SILICON # Whichever makes sense for you
BIN=~/bin/sandbox #Or somewhere else on your $PATH

wget -O "$BIN" https://github.com/franklin-ross/sandbox/releases/latest/download/$ARCH
chmod +x "$BIN"
# macOS only — remove Gatekeeper quarantine
xattr -d com.apple.quarantine "$BIN"
# Initialise global config at ~/.sandbox/
sandbox config init
# Review the default firewall and sync configuration
code ~/.sandbox/config.yaml
```

### Building From Source

Alternatively, clone the repo and install from source:

```bash
task install        # Install to ~/bin/sandbox
sandbox config init # etc.
```

## Usage

On first launch, the tool builds the Docker image automatically, which may take some time.

Claude credentials live inside the sandbox, so you need to log in once for each sandbox.

```bash
# Global initialisation (run once)
sandbox config init

# Open Claude in a directory (with --dangerously-skip-permissions)
sandbox claude project/
# Pass args through to Claude
sandbox claude . -- -p "fix the failing tests"
# Open a shell in a running sandbox
sandbox shell ~/projects/myapp

# Open VSCode inside the sandbox
sandbox code .

# List running sandboxes
sandbox ls
# Stop a running sandbox
sandbox stop .
# Remove a sandbox (stops it first if running)
sandbox rm .
# Forcibly copy files, update firewalls, and run on_sync scripts inside
# the sandbox. Runs automatically on start, but can be used to update a
# running sandbox without restarting.
sandbox sync project/
```

### Parent Sandbox Discovery

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
    HELLO: $HELLO # expanded from host env
    # BEWARE: These environment variables are readable inside the container,
    # bringing secrets or credentials into the container this way can defeat
    # the purpose of using a sandbox.

firewall:
    allow:
        - domain: api.example.com
        - domain: api2.example.com
          ports: [22, 80, 443]
        - cidr: 10.0.0.0/8

# Run shell commands whenever the config or any sync'd files change
on_sync:
    - name: install deps
      cmd: npm install -g my-tool
    - cmd: chmod 600 ~/.ssh/*
      root: true

# MCP tools allowing the unprivileged sandbox to run commands in the privileged host environment
# without having access to the credentials there.
host_tools:
    - name: view_pr
      description: View information and comments for a pull request in the current repository
      cmd: gh pr view ${pr_number} --comments
      args:
          - name: pr_number
            type: integer
            min: 0
```

Whenever this config or any of the synced files change, the next command resynchronises everything into the sandbox.

See [specs/sandbox-config.spec.md](specs/sandbox-config.spec.md) for full details.

### Host Tools

Host tools let the agent inside the sandbox trigger a limited set of pre-configured commands on the host machine. Tools can accept typed, validated arguments that are shell-quoted before substitution, so the agent can take privileged action without ever coming near credentials or injecting shell metacharacters.

When you use `sandbox claude`, the tool automatically exposes host tools as MCP tools, so Claude sees them as first-class tool calls with full input schemas.

**SAFETY NOTE**: Host tools are the one channel that deliberately runs commands _outside_ the sandbox, so they are the primary escalation surface. Read [Security Model](#security-model) before defining any.

The framework shell-quotes every argument, so argument _injection_ is not the risk. The risk is the command itself. Each tool runs as `sh -c` **on the host**, in the workspace directory — and the agent inside the sandbox can write to that directory freely (it is the same bind mount, owned by your UID). A tool is unsafe whenever its command executes or resolves anything out of that directory, because the agent controls those bytes:

- **VCS tools** (`git …`, `gh …`) read `.git/config` (`core.hooksPath`, `core.fsmonitor`, aliases, pager) and run `.git/hooks/*` — all agent-writable. `gh` shells out to `git`. Either path hands the agent arbitrary host code execution, with no malicious argument required.
- **Build / task runners** (`npm`, `yarn`, `make`, `task`, `./deploy.sh`) execute scripts out of agent-controlled `package.json` / `Makefile` / `Taskfile.yml` / the script file itself.
- Anything that loads a config file, plugin, or binary resolved relative to the workspace cwd.

The intended use is _"take some action without putting my credentials into the sandbox"_ using typed, validated arguments (an issue ID, an environment name) acting on a resource that lives entirely **outside** the workspace. It is **not** _"let the agent run my build on the host"_; the agent can already run build scripts inside the sandbox.

If a tool must invoke `git`/`gh`, neutralise the workspace it would otherwise inherit — leave the directory and disable inherited config:

```yaml
host_tools:
    - name: view-github-issue
      description: View a GitHub issue
      # cd out of the (agent-writable) workspace and ignore any .git config it
      # planted, so an injected agent can't redirect hooks or fsmonitor onto the host.
      cmd: cd "$HOME" && GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null gh issue view ${id} --repo myorg/myrepo --comments
      args:
          - name: id
            type: integer
            min: 1
```

Keep tools simple, focused, and easy to grok, with the simplest args possible. Anything that doesn't genuinely need to run outside the sandbox should be a binary or shell script _inside_ it.

```yaml
host_tools:
    - name: deploy
      description: Deploy the app to a target environment
      cmd: ./deploy.sh ${env} ${tag}
      args:
          - name: env
            description: Target environment
            enum: [staging, prod]
          - name: tag
            description: Git tag to deploy
            regex: '^v\d+\.\d+\.\d+$'
    - name: scale
      description: Scale the api deployment
      cmd: kubectl scale --replicas=${n} deploy/api
      args:
          - name: n
            type: integer
            min: 0
            max: 20

# Optional: override the default daemon port (9847)
# host_tool_port: 9848
```

#### Arg Fields

| Field                       | Description                                                                                              |
| --------------------------- | -------------------------------------------------------------------------------------------------------- |
| `name`                      | Arg name (required). Referenced in `cmd` as `${name}`.                                                   |
| `description`               | Human-readable description for the MCP input schema.                                                     |
| `type`                      | `string` (default), `integer`, `number`, or `boolean`.                                                   |
| `required`                  | Defaults to `true`. Set `false` for optional args.                                                       |
| `default`                   | Default value when the arg is not provided.                                                              |
| `enum`                      | Restrict to a set of allowed values.                                                                     |
| `regex`                     | Value must match this regular expression.                                                                |
| `min` / `max`               | Numeric range bounds (for `integer` and `number` types).                                                 |
| `min_length` / `max_length` | String length bounds.                                                                                    |
| `url`                       | URL constraint object (see below).                                                                       |
| `validate`                  | Shell command run on the host to validate the value. Receives the value on stdin; non-zero exit rejects. |

#### URL Constraints

The `url` field enables URL-specific validation with SSRF protection:

| Field               | Description                                                                                                                                                                                                                                                                                                                |
| ------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `schemes`           | Allowed URL schemes. Defaults to `["https"]`.                                                                                                                                                                                                                                                                              |
| `hosts`             | Host allowlist. Patterns are literal (not glob): an exact host (`api.github.com`) or a leading-dot suffix (`.github.io`) matching that domain and its subdomains. The loader rejects wildcards. Omitting it lets the agent pass any URL — the loader warns, since an allowlist is the real defence against SSRF/rebinding. |
| `path_prefix`       | URL path must start with this prefix.                                                                                                                                                                                                                                                                                      |
| `block_private_ips` | Defaults to `true`. Blocks private, loopback, link-local, and metadata IPs after DNS resolution.                                                                                                                                                                                                                           |

**Pinning the resolved IP (DNS-rebinding protection).** `block_private_ips`
runs at validation time, but a plain `curl ${u}` re-resolves DNS when it runs,
so a hostile name could resolve to a safe IP during validation and a private
one (e.g. `169.254.169.254`) at fetch time. To close this gap, every `url` arg
exposes a derived `${<name>_resolve}` placeholder holding the validated
`host:port:ip`. Feed it to the client so the fetch connects to exactly the
address the framework checked, and disable redirects:

```yaml
- name: fetch
  description: Fetch a file from an allowlisted host
  cmd: curl -fsS --resolve ${url_resolve} --max-redirs 0 ${url}
  args:
      - name: url
        url:
            hosts: [".github.io", objects.githubusercontent.com]
```

`${url_resolve}` is curl's (and wget2's) `--resolve` format. For clients that
pin differently, each `url` arg also exposes the components:

| Placeholder         | Value          | Example use                           |
| ------------------- | -------------- | ------------------------------------- |
| `${<name>_resolve}` | `host:port:ip` | `curl --resolve ${u_resolve} ${u}`    |
| `${<name>_ip}`      | validated IP   | a client taking an IP + `Host` header |
| `${<name>_host}`    | hostname       | `--header "Host: ${u_host}"`          |
| `${<name>_port}`    | port           | —                                     |

These derived placeholders exist only when `block_private_ips` is on (the
default). TLS still validates the original hostname, so pinning the IP does not
weaken certificate checks.

All arg values are shell-quoted before substitution into `cmd`, preventing injection.

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

| Service    | Domains                                                                            |
| ---------- | ---------------------------------------------------------------------------------- |
| Claude API | api.anthropic.com, claude.ai, statsig.anthropic.com, sentry.io                     |
| npm / Yarn | registry.npmjs.org, registry.yarnpkg.com, repo.yarnpkg.com, registry.npmmirror.com |
| Go         | proxy.golang.org, sum.golang.org, storage.googleapis.com                           |
| Rust       | crates.io, static.crates.io, index.crates.io, static.rust-lang.org                 |
| Ruby       | rubygems.org, api.rubygems.org, index.rubygems.org                                 |
| PyPI       | pypi.org, files.pythonhosted.org                                                   |
| CDNs       | cdn.jsdelivr.net, dl-cdn.alpinelinux.org, deb.nodesource.com                       |
| Cypress    | download.cypress.io, cdn.cypress.io                                                |
| Playwright | cdn.playwright.dev, playwright.download.prss.microsoft.com                         |

The firewall blocks everything else. It allows DNS so processes inside the container can still resolve hostnames.

**GitHub is not allowed by default.** It is a general-purpose host for untrusted user content, which makes it both a broad data-exfiltration channel (push, gist, issue) and an arbitrary-payload source (`raw.githubusercontent.com`, `codeload`, `ghcr.io`). Add it per-workspace when a project needs it:

```yaml
firewall:
    allow:
        - domain: github.com
          ports: [22, 80, 443]
        - domain: api.github.com
        - domain: raw.githubusercontent.com
        - domain: objects.githubusercontent.com
        - domain: codeload.github.com
```

## Security Model

The sandbox is designed to minimise the chance of an unattended agent reading malicious instructions, or acting on them in a meaningful way. It cannot make an agent entirely safe. If you're truly paranoid, look elsewhere.

The container is unprivileged (`no-new-privileges`, agent user), outbound traffic is firewalled to an allowlist, your credentials never enter the container, the host-tool control plane is unreachable from the container (host-only Unix socket + a control token in an unmounted path), and the workspace's `.sandbox/` is masked so the agent cannot rewrite the config that drives the host.

### What it does _not_ protect

It cannot protect the host from **code the host later executes out of a workspace the agent had write access to.** The workspace is a read-write bind mount, so for the duration of a session the agent can write any file in the tree, including dotfiles and VCS metadata. Two consequences:

- **`git commit` / `git push` from the host is host code execution.** An agent can plant `.git/hooks/*`, or set `core.hooksPath` / `core.fsmonitor` / an alias / a pager in `.git/config`, and the payload runs the next time _you_ run git in that repo on the host — not inside the sandbox. This requires no host tools at all; it is inherent to mounting a repo you also operate on from the host.
- **Tracked build scripts are host code execution.** `package.json` scripts, `Makefile`, `Taskfile.yml`, `.husky/*`, lint/test configs, etc. all run on the host if you build, test, or run the project there afterwards.

None of this is visible in `git diff` — hooks and most config live outside the working tree, and a changed npm script is one line in a file you may not read. **Reviewing the diff is not always sufficient.** Treat a post-session workspace as untrusted code if you intend to execute it on the host.

This is a property of the shared read-write mount, not a fixable bug: the agent shares your UID on the mount, and the local `.git/config` it can poison overrides any global git setting you might use to defend, so the sandbox cannot carve out a protected `.git/`.

### Host tools

[Host tools](#host-tools) are the one channel that deliberately runs commands on the host. They are guarded (typed/validated/shell-quoted arguments, per-session token, host-only registration), but the command you configure runs in the agent-writable workspace directory, so a poorly chosen tool re-opens exactly the execution path above. See the safety note in that section before defining any.

## How it Works

The `sandbox` binary embeds the Docker image files via `go:embed`. When you run `sandbox start`, it:

1. Writes the embedded Dockerfile and scripts to a temp directory
2. Builds the image (if not already built)
3. Runs the container with `--cap-add=NET_ADMIN` (for iptables)
4. Mounts your workspace into the container
5. Sets up iptables firewall rules via the entrypoint, then sleeps
