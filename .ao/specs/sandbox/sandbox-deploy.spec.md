---
id: sandbox-deploy
title: Smoother sandbox deployment
status: draft
---

# Smoother sandbox deployment

## Background

Two pain points make the sandbox deploy cycle slower than it needs to be:

1. **Claude credentials lost on container rebuild.** A named Docker volume
   (`ao-sandbox-claude-creds`) persists the `/home/agent/.claude` directory
   across container restarts, but removing and recreating the container (e.g.
   after an image rebuild) causes Claude to stop recognising the stored
   credentials. The likely cause is that Docker assigns a random hostname to
   each new container and the Claude CLI ties auth tokens to machine identity.
   The user has to `claude /login` again every time.

2. **Container must be removed and recreated for workflow changes.** The
   workflow binary is baked into the Docker image at build time
   (`COPY workflow-linux /usr/local/bin/workflow`). Any change to workflow code
   requires rebuilding the image, removing the old container, and starting a
   new one — a multi-step process that also triggers problem 1.

## What needs to change

### 1. Stable container hostname

Pass `--hostname` to `docker run` so that every container for a given workspace
gets a deterministic, stable hostname. This prevents the Claude CLI from
treating a recreated container as a new machine.

In `docker.go`, the `ensureRunning` function currently builds the `docker run`
command without a hostname flag. Add `--hostname` using the same base name as
the container (e.g. `ao-sandbox-myproject`):

```go
cmd := exec.Command("docker", "run", "-d",
    "--name", name,
    "--hostname", name,          // <-- new
    "--label", labelSel,
    ...
)
```

### 2. `sandbox update` command

Add a new subcommand that pushes all embedded runtime files into a running
container without rebuilding the image or recreating the container:

```
sandbox update [path]
```

- Resolves the workspace path the same way other commands do (defaults to `.`).
- Finds the running container for that workspace.
- Exits with an error if the container is not running.
- Pushes every embedded file that the Dockerfile normally COPYs:

    | Embedded var       | Container path            | Owner | Mode |
    | ------------------ | ------------------------- | ----- | ---- |
    | `workflowBinary`   | `/usr/local/bin/workflow` | root  | 0755 |
    | `entrypointScript` | `/opt/entrypoint.sh`      | root  | 0755 |
    | `firewallScript`   | `/opt/init-firewall.sh`   | root  | 0755 |

- For each file: write the embedded bytes to a temp file on the host with
  mode 0755, then `docker cp` it into the container. `docker cp` runs as
  root inside the container, so ownership lands as root — matching what the
  Dockerfile sets. Clean up the temp file after each copy.
- After copying the firewall script, re-run it inside the container:
  `docker exec <container> sudo /opt/init-firewall.sh`. This applies any
  whitelist changes to the running iptables rules without a restart.
- Print a summary of what was pushed.

The entrypoint script only runs at container start, so changes to it take
effect on the next `docker restart`. The command prints a note about this
when the entrypoint has changed (or always — simpler).

This means the dev cycle becomes:

```
task build:sandbox   # rebuild sandbox binary (embeds new workflow + scripts)
sandbox update .     # push everything into running container
```

No image rebuild, no container removal, no re-login.

### 3. Files to change

- `sandbox/cmd/docker.go` — Add `--hostname`, name to the `docker run` call
  in `ensureRunning`.
- `sandbox/cmd/update.go` — New file. Cobra command implementing
  `sandbox update`. For each embedded file: write to a host temp file with
  mode 0755, `docker cp` into the container, remove temp file. After
  copying the firewall script, `docker exec ... sudo /opt/init-firewall.sh`
  to apply it live.
- `sandbox/cmd/root.go` — Register the update command.

## Acceptance criteria

- `docker inspect` of a newly created sandbox container shows a hostname
  matching the container name.
- Claude credentials in the named volume survive a full remove-rebuild-recreate
  cycle without requiring re-login (given the stable hostname is the fix).
- `sandbox update .` copies the workflow binary, entrypoint, and firewall
  script into a running container. The workflow binary works immediately.
- All pushed files have mode 0755 and root ownership inside the container,
  matching the Dockerfile originals. The `agent` user can execute them.
- After pushing the firewall script, `sandbox update` re-runs it so iptables
  rules reflect any whitelist changes without a container restart.
- `sandbox update .` exits with a clear error when no container is running for
  the workspace.
- All existing tests pass.

## Out of scope

- Hot-reloading (the user still runs the update command after rebuilding).
- Updating the Dockerfile itself (base packages, system deps). That still
  requires a full image rebuild — it changes rarely.
- Bind-mounting files from the host at `docker run` time (requires knowing
  host paths at container creation, fragile across machines and workspaces).
