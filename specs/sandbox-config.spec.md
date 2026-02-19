# sandbox configuration

## Purpose

The sandbox is a Docker-based isolated environment for running code and
AI agents. Its behaviour — what files are present, what network access
is allowed, and what environment is configured — is controlled through
a user-editable YAML config rather than hardcoded values in the binary.

Users manage their sandbox configuration in `~/.sandbox/`. Per-workspace
overrides allow project-specific customisation without affecting the
global defaults.

## Configuration

### Config file

The sandbox reads configuration from two YAML files, merged at load time:

| Location | Scope |
|----------|-------|
| `~/.sandbox/config.yaml` | Global — applies to all sandboxes |
| `<workspace>/.sandbox/config.yaml` | Per-workspace — overrides global |

### Merge semantics

When both global and workspace configs exist, they merge as follows:

- **`env`**: workspace values override global values for the same key.
  Keys present only in global are preserved.
- **`sync`**: workspace rules with the same `dest` replace the global
  rule for that destination. Rules with different destinations are
  additive.
- **`firewall.allow`**: purely additive. Both global and workspace
  entries are included.
- **`on_sync`**: purely additive. Global hooks run first, then
  workspace hooks.

### Schema

```yaml
# Files to sync into the container
sync:
  - src: /path/to/file           # exact file path
    dest: /container/path         # container destination
    mode: "0755"                  # optional, defaults to "0644"
    owner: "agent:agent"          # optional, defaults to "agent:agent"
  - src: /path/to/scripts/*.sh   # glob pattern (filepath.Glob)
    dest: /home/agent/scripts/   # trailing slash = directory target

# Environment variables for sandbox sessions
env:
  FOO: bar                                 # literal value
  SECRET: value
  GITHUB_TOKEN: $GITHUB_TOKEN              # expanded from host env at sync time

# Firewall allowlist
firewall:
  allow:
    - domain: api.example.com              # defaults to ports 80, 443
    - domain: staging.example.com
      ports: [443, 8443]                   # custom port list
    - cidr: 10.0.0.0/8                     # raw IP/CIDR range
      ports: [443]                         # optional port restriction

# Commands to run inside the container after every sync
on_sync:
  - cmd: npm install                       # required — shell command
    name: install deps                     # optional — label for status output
  - cmd: chmod 600 ~/.ssh/*
    root: true                             # optional — run as root (default: false)
```

## `sandbox init`

`sandbox init` creates the initial configuration directory and default
config file:

- Creates `~/.sandbox/config.yaml` with the default firewall
  allowlist (see Firewall section).
- Creates the `~/.sandbox/home/` directory.
- If the config file already exists, prints a message and exits
  without overwriting.

## `sandbox sync`

`sandbox sync` forces a re-sync of all files into a running container,
regardless of whether the hash has changed. This is useful after
editing config or home directory files to apply changes immediately.

## Error handling

Config errors (malformed YAML, unreadable sync source, missing glob
matches) print a warning to stderr but do not abort the sync. The
container is still useful with partial configuration. Errors in
individual sync items are reported but do not prevent other items from
being synced.

## File syncing

### Convention-based home directory

Files placed in `~/.sandbox/home/` are synced to `/home/agent/` in
the container, preserving directory structure:

```
~/.sandbox/home/
  .gitconfig          → /home/agent/.gitconfig
  .vimrc              → /home/agent/.vimrc
  bin/
    my-script         → /home/agent/bin/my-script  (mode 0755)
  .config/
    nvim/
      init.vim        → /home/agent/.config/nvim/init.vim
```

- All files receive `agent:agent` ownership.
- Files under `home/bin/` receive mode `0755`.
- All other files receive mode `0644`.

### Explicit sync rules

The `sync` section of `config.yaml` defines additional files to copy
into the container. Each rule specifies a source path on the host and
a destination path in the container.

When `src` contains glob characters (`*`, `?`, `[`), it is expanded
with `filepath.Glob()`:

- If `dest` ends with `/`, each matched file is placed in that
  directory keeping its basename.
- If `dest` does not end with `/` and the glob matches exactly one
  file, it is treated as a direct file mapping.
- Multiple glob matches to a non-directory `dest` is an error.

Defaults for optional fields: `mode` is `"0644"`, `owner` is
`"agent:agent"`.

### Sync pipeline

All synced content — built-in embedded assets, generated files, and
user-configured files — passes through a unified pipeline. Items are
processed in this order:

1. Built-in embedded assets (entrypoint, firewall script) — owned by
   `root:root`, mode `0755`.
2. Generated firewall rules script (from `firewall.allow` config).
3. Generated environment file (from `env` config).
4. Generated ZSH theme file (from host detection).
5. Custom oh-my-zsh theme file (if present on host).
6. Convention-based `~/.sandbox/home/` files.
7. Explicit sync rules from config (with glob expansion).

Later items with the same destination override earlier items.

### Change detection

A SHA-256 hash covers all synced content: embedded assets (entrypoint,
firewall script), the merged config, home directory files, explicit
sync source files, and on_sync hook definitions. The hash is stored at
`/opt/sandbox-sync.sha256` in the container. Sync is skipped when the
hash matches the previous sync, unless a force sync is requested (via
`sandbox sync`).

## Firewall

### Structure

The firewall script (`init-firewall.sh`) runs at container start via
the entrypoint. It sets up baseline iptables rules — allow loopback,
DNS, and established connections — then sources a generated rules file
at `/opt/sandbox-firewall-rules.sh` if it exists, then applies a default
REJECT policy.

There are no hardcoded domain allowlists in the firewall script. All
allowed domains and CIDRs are defined in `config.yaml` and compiled
into the generated rules file during sync.

### Rules generation

For each `domain` entry: the generated script resolves the domain via
`dig`, follows CNAMEs one level deep, and adds iptables ACCEPT rules
for each resolved IP. Default ports are 80 and 443; a `ports` field
overrides the defaults.

For each `cidr` entry: the script adds iptables ACCEPT rules directly.
If `ports` is specified, traffic is restricted to those ports. If
`ports` is omitted, all ports are allowed to the CIDR.

### Default allowlist

`sandbox init` generates a config with the following default domains:

| Category | Domains |
|----------|---------|
| Claude API | `api.anthropic.com`, `api.claude.ai`, `claude.ai`, `statsig.anthropic.com`, `sentry.io` |
| npm / yarn / corepack | `registry.npmjs.org`, `registry.yarnpkg.com`, `repo.yarnpkg.com`, `registry.bun.sh`, `registry.npmmirror.com` |
| Go | `proxy.golang.org`, `sum.golang.org`, `storage.googleapis.com` |
| Rust | `crates.io`, `static.crates.io`, `index.crates.io`, `static.rust-lang.org` |
| Ruby | `rubygems.org`, `api.rubygems.org`, `index.rubygems.org` |
| PyPI | `pypi.org`, `files.pythonhosted.org` |
| GitHub | `github.com`, `api.github.com`, `raw.githubusercontent.com`, `objects.githubusercontent.com`, `codeload.github.com`, `pkg-containers.githubusercontent.com`, `ghcr.io` |
| Cypress | `download.cypress.io`, `cdn.cypress.io` |
| Playwright | `cdn.playwright.dev`, `playwright.download.prss.microsoft.com` |
| CDNs | `cdn.jsdelivr.net`, `dl-cdn.alpinelinux.org`, `deb.nodesource.com` |

### Change lifecycle

When the firewall rules file changes during a sync, the firewall
script is re-run inside the container via `docker exec`. The script
resolves domains via DNS and applies iptables rules. If reinitialisation
takes more than a few seconds, a progress message is printed. On
failure, a warning is printed but the sync continues — the container
is still usable, just with stale firewall rules.

## Environment variables

Environment variables defined in the `env` section of `config.yaml`
are injected into the container two ways:

1. **Shell profile**: a generated file at `/home/agent/.sandbox-env`
   contains `export KEY=value` lines. The container's `.zshrc` sources
   it: `[ -f ~/.sandbox-env ] && source ~/.sandbox-env`. This covers interactive
   shell sessions.

2. **Exec flags**: env vars are passed via `-e` flags on `docker exec`
   calls. This covers non-shell command execution (e.g., `sandbox claude`).

### Dynamic values

Values prefixed with `$` are expanded from the host environment at
sync time. For example:

```yaml
env:
  GITHUB_TOKEN: $GITHUB_TOKEN
  MY_VAR: literal-value
```

`$GITHUB_TOKEN` is read from the host's environment when the sync
runs. If the host variable is unset, the env var is omitted from the
generated env file (not set to empty). Literal values (no `$` prefix)
are used as-is.

## Post-sync hooks

The `on_sync` section defines shell commands that run inside the
container after every sync completes (after files are copied and
firewall rules are applied). This is useful for installing
dependencies, fixing permissions, or other setup that needs to happen
each time the sandbox environment is refreshed.

### Fields

| Field  | Required | Default | Description |
|--------|----------|---------|-------------|
| `cmd`  | yes      | —       | Shell command passed to `sh -c` inside the container |
| `name` | no       | `cmd`   | Human-readable label shown in the status line during sync |
| `root` | no       | `false` | Run as `root` instead of `agent` |

Only two users are available: `agent` (default) and `root`. The
`root` flag is a boolean rather than an arbitrary user string to keep
things simple and safe.

### Execution

Hooks run sequentially in the order they appear in the merged config
(global hooks first, then workspace hooks). Each hook runs via:

```
docker exec -u <user> -w /home/agent <container> sh -c "<cmd>"
```

If any hook fails (non-zero exit), the sync aborts immediately and
the error — including the hook's combined stdout/stderr — is reported.
The sync hash is **not** written, so the next sync will re-run all
hooks.

### Change detection

Hook definitions (cmd, name, root) are included in the sync hash.
Changing a hook's command or flags in config triggers a re-sync and
re-execution of all hooks. The actual hook output is not hashed.

### Examples

```yaml
# Install JS dependencies after sync
on_sync:
  - cmd: npm install
    name: install deps

# Fix SSH key permissions (needs root)
  - cmd: chmod 600 /home/agent/.ssh/*
    root: true
    name: fix ssh perms

# Run a project-specific setup script
  - cmd: ./scripts/setup.sh
    name: project setup
```

## ZSH theme

The host's ZSH theme is detected and synced into the container via a
source-file pattern rather than modifying `.zshrc` directly.

A file `/home/agent/.sandbox-zsh-theme` is synced containing the theme
assignment (e.g., `ZSH_THEME="robbyrussell"`). The container's `.zshrc`
sources this file before oh-my-zsh loads, so the theme variable is set
in time for oh-my-zsh to read it:

```
[ -f ~/.sandbox-zsh-theme ] && source ~/.sandbox-zsh-theme
```

Custom oh-my-zsh theme files (from `~/.oh-my-zsh/custom/themes/`) are
copied to the same location in the container.

## Container lifecycle

### Naming and identity

Each workspace gets a container named `sandbox-<basename>` (derived
from the workspace directory name). The container's hostname is set to
the same value, giving each workspace a stable machine identity across
container restarts and recreations.

### Credential persistence

A named Docker volume (`sandbox-creds`) is mounted at
`/home/agent/.claude` in every container. This persists Claude CLI
credentials across container restarts and image rebuilds. The stable
hostname ensures the Claude CLI does not treat a recreated container
as a new machine, so credentials remain valid without re-authenticating.

### Sync without rebuild

The entrypoint and firewall scripts are embedded in the sandbox Go
binary and synced into running containers at startup. Changes to these
files do not require an image rebuild — a `sandbox sync` or container
restart picks them up. Additional binaries (such as the workflow CLI)
are installed via the user's `~/.sandbox/home/` directory or
explicit sync rules.

## Container image

### Chromium

The sandbox image includes Chromium for headless testing with Karma,
Cypress, and Storybook. The `CHROME_BIN` and `CHROMIUM_BIN` environment
variables are set to `/usr/bin/chromium`, the browser binary installed
in the image.

### Installed toolchains

The image is based on Debian Bookworm and includes: Node.js, Go, Rust,
Ruby, Python 3, and standard CLI tools (ripgrep, jq, fzf, tmux, git,
curl, zsh). Claude Code CLI is pre-installed. Corepack is enabled with
yarn pre-activated.

## Out of scope

- Docker run flags (extra volumes, ports, capabilities) from config.
  Only sync-time changes are supported.
- Build-time image customisation from config.
- Per-container firewall isolation. All containers share the same
  config-derived rules.
- GUI browser support. Chrome is headless only.
