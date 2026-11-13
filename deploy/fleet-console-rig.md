# Bring up `fleet-console-rig` on another box

`deploy/fleet-console-rig.rigbundle` is the OpenRig team that works on this repository:

| Seat | Runtime | Model (through agentgateway) | Role |
|---|---|---|---|
| `dev-builder-claude@fleet-console-rig` | Claude Code | `claude/default` | builds |
| `dev-builder-pi@fleet-console-rig` | Pi | `agentgateway/pi/default` | builds |
| `dev-builder-omp@fleet-console-rig` | Oh My Pi | `litellm/pi/default` | builds (full bypass, see below) |
| `dev-review@fleet-console-rig` | Codex | `codex/default` | reviews |

The bundle is self-contained: its agents are packed inside it (`local:agents/...`), so it doesn't depend on
where OpenRig is installed. It carries no logins or keys. Every model goes through a local
[agentgateway](https://agentgateway.dev) at `http://localhost:4000`, so that has to be running first.

Check the archive before you start:

```bash
cd <this checkout>
(cd deploy && sha256sum -c <(echo "$(cat fleet-console-rig.rigbundle.sha256)  fleet-console-rig.rigbundle"))
```

## 1. Prerequisites

- OpenRig CLI with its daemon running: `rig status` shows the daemon `running`.
- Claude Code, Codex, Pi and Oh My Pi (`omp`) installed and on the `PATH` of the user the daemon runs as.
- A clone of `dinhtrung/fleet-console`. Below, `$REPO` is its absolute path.
- `gh` signed in with push access, since the builders push branches and open PRs.

## 2. agentgateway

The seats only know three model names; agentgateway maps them to real providers.

1. Install agentgateway and run it on port 4000 (`llm.port: 4000`).
2. Define the virtual models `claude/default`, `pi/default` and `codex/default` in its config. Each one
   routes to a real model with failover, for example DeepSeek direct, then a paid OpenRouter model.
3. Put the provider keys in an env file that only you can read (`chmod 600`), for example
   `OPENROUTER_API_KEY` and `DEEPSEEK_API_KEY`, and reference them from the config as `$NAME`. Never
   write a key into the config itself.
4. Make the health rule fail over on `400` and `404` as well as `401`, `402`, `429` and `>= 500`. A
   retired model answers 404 and a wrong model name answers 400; without them, failover never triggers.
5. Check it:

   ```bash
   curl -s localhost:4000/v1/models                      # lists the three virtual models
   curl -s localhost:4000/v1/chat/completions -H 'Content-Type: application/json' \
     -d '{"model":"pi/default","messages":[{"role":"user","content":"Reply with exactly: ok"}],"max_tokens":32}'
   ```

## 3. Point the runtimes at the gateway

- **Claude Code**: nothing to do. The bundle sets `ANTHROPIC_BASE_URL=http://localhost:4000` and every
  model slot to `claude/default` in the builder's `env:`.
- **Codex**: in the daemon user's Codex home (`$CODEX_DIR`, see step 4), add to `config.toml`:

  ```toml
  model_provider = "agentgateway"
  model = "codex/default"

  [model_providers.agentgateway]
  name = "agentgateway"
  base_url = "http://localhost:4000/v1"
  ```

- **Pi**: OpenRig gives each managed Pi seat its **own** Pi folder. It does not read the user's `.pi/agent`. That
  folder is under the daemon's home, which can differ from box to box. Find it first (step 4).
  The seat needs this `models.json` there:

  ```json
  {
    "$schema": "https://pi.dev/schemas/models.schema.json",
    "providers": {
      "agentgateway": {
        "baseUrl": "http://localhost:4000/v1",
        "api": "openai-completions",
        "apiKey": "agentgateway",
        "models": [{ "id": "pi/default" }, { "id": "codex/default" }]
      }
    }
  }
  ```

- **Oh My Pi**: OMP has no agentgateway provider, so it uses its LiteLLM-compatible one. Like Pi, each managed
  OMP seat has its own folder (it runs with that folder as `HOME`). The seat needs an `agent/.env` there; see
  step 4 for the path:

  ```bash
  LITELLM_BASE_URL=http://127.0.0.1:4000/v1
  LITELLM_API_KEY=agentgateway
  ```

## 4. Find the daemon's user and home on this box

Nothing in the bundle depends on the `$HOME` of the box it was made on. Its agents are packed inside it,
and `--cwd` supplies the repository. The only absolute paths left are provenance notes in `bundle.yaml`
(`original_ref`, `source_host`), which install ignores.

What does depend on a home is where the **daemon** looks for each runtime's configuration. That's the
`$HOME`, `$OPENRIG_HOME` and `$CODEX_HOME` of the user the daemon runs as, which need not be yours or
`/home/<you>`. Read them from the running daemon instead of assuming:

```bash
DAEMON_PID=$(pgrep -f '@openrig/cli/daemon/dist/index.js' | head -1)
DAEMON_USER=$(ps -o user= -p "$DAEMON_PID")
echo "daemon pid=$DAEMON_PID user=$DAEMON_USER"
# Same user as you: read its environment directly (another user: prefix with sudo).
tr '\0' '\n' < /proc/$DAEMON_PID/environ | grep -E '^(HOME|OPENRIG_HOME|CODEX_HOME|PI_CODING_AGENT_DIR)='
```

Then set these for the steps below, filling in what the daemon reported. `OPENRIG_HOME` defaults to
`$HOME/.openrig` and `CODEX_HOME` to `$HOME/.codex` when the daemon doesn't set them:

```bash
export DAEMON_HOME=/the/HOME/it/printed          # e.g. /srv/rig or /home/ops
export RIG_HOME=/its/OPENRIG_HOME                   # if it printed none: "$DAEMON_HOME/.openrig"
export CODEX_DIR=/its/CODEX_HOME                    # if it printed none: "$DAEMON_HOME/.codex"
PI_SEAT="$RIG_HOME/state/pi/dev-builder-pi@fleet-console-rig/agent"
OMP_SEAT="$RIG_HOME/state/omp/dev-builder-omp@fleet-console-rig/agent"
```

Use these everywhere instead of `~`:

- **Codex** (step 3): edit `$CODEX_DIR/config.toml`.
- **Pi**: the seat folder is `$PI_SEAT`. If you link a personal Pi setup into it (below), use the daemon
  user's Pi folder, `$DAEMON_HOME/.pi/agent`.
- **Claude Code**: the bundle sets the gateway URL and models itself; it uses the daemon user's Claude home
  only for its own settings.
- **agentgateway**: it can run as any user. The seats only need `http://localhost:4000` to answer on this box.
- **The repository** for `--cwd`: any absolute path the daemon user can read and write.

If the daemon runs as another user, run every step as that user, so the files it reads are its own and it
can write to them: `sudo -iu "$DAEMON_USER"`.

Prepare the Pi seat **before** the first launch. Otherwise Pi exits with
`Model "agentgateway/pi/default" not found`. OpenRig creates the folder at launch but keeps the files it finds
there:

```bash
mkdir -p "$PI_SEAT"
cp <the models.json from step 3> "$PI_SEAT/models.json"

mkdir -p "$OMP_SEAT"
( umask 077; printf 'LITELLM_BASE_URL=http://127.0.0.1:4000/v1\nLITELLM_API_KEY=agentgateway\n' > "$OMP_SEAT/.env" )
```

Optional: to give the seat your own Pi packages, extensions and MCP servers, link them from your Pi folder.
Leave `auth.json`, `AGENTS.md`, `skills/` and `sessions/` alone, because OpenRig manages those:

```bash
for n in settings.json mcp.json extensions npm; do ln -sfn "$DAEMON_HOME/.pi/agent/$n" "$PI_SEAT/$n"; done
```

## 5. Install and launch

Preview first, then install. `--target` is where the unpacked spec lives. `--cwd` is the repository the seats
work in. It's required, because the seats declare `.` as their folder.

```bash
rig bundle install "$REPO/deploy/fleet-console-rig.rigbundle" --target "$DAEMON_HOME/rigs/fleet-console-rig" --cwd "$REPO" --plan
rig bundle install "$REPO/deploy/fleet-console-rig.rigbundle" --target "$DAEMON_HOME/rigs/fleet-console-rig" --cwd "$REPO"
```

If a team named `fleet-console-rig` already runs on that box, the preview refuses. `--target` doesn't rename a
team. Stop the existing one first with `rig down fleet-console-rig` (that archives it), or keep it and don't install.

Wait for all three seats to report `ready`:

```bash
rig ps --nodes --rig fleet-console-rig
```

## 6. If the Pi seat still fails

`rig ps` shows it `attention_required` and its pane says the model isn't found:

```bash
# The pane shows the folder the runner really used:
rig capture dev-builder-pi@fleet-console-rig --lines 200 | grep -o "state-root '[^']*'"
# Put models.json in <that state root>/dev-builder-pi@fleet-console-rig/agent/, then relaunch only that seat:
rig seat launch dev-builder-pi@fleet-console-rig --fresh --stop --reason "add the agentgateway models.json"
```

`rig seat continue` does not relaunch Pi; use `rig seat launch --fresh --stop`.

## 7. Work and safety rules

- `dev.builder-omp` runs with `permission_policy: builtin:yolo` (OMP `--approval-mode yolo`): it runs shell
  commands as the daemon user **without asking**. OMP has no middle setting. Its default, `always-ask`, cancels
  every command in a managed pane, because nobody there can answer. The install preview names it as `full_bypass`.
  Remove that seat from the spec if you don't want an agent with that access on the box.

- The builders work only in their row's worktree and push their feature branch. They never commit to or push
  `main`. Protect `main` on GitHub: require a pull request before merging, applied to admins too.
- fleet-console's dispatcher sends review rows to `dev-review@<rig>`, so keep the reviewer's member id `review`.
- Owner decisions go to a registered person (`rig gateway human add`), not `human@kernel`.
- Restoring the same team later keeps the Pi seat's folder: `rig down fleet-console-rig`, then
  `rig up fleet-console-rig --existing`. A new rig name, or an account switch, gives the Pi seat a new, empty
  folder; repeat step 4 for it.
