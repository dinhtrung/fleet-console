# Example project — step-by-step guide

A complete end-to-end run: from a fresh checkout of any project, through
agentgateway.dev configuration, rig initialisation, fleet-console dispatch, to a
Claude-created epic with the starting prompt. Everything here is copy-pasteable
and each step states the success check.

---

## 0. What you will have at the end

```
example-project/          # your project checkout
  rig.yaml                # the rig spec (created in step 6)
  .fleet-console/         # per-project config (created in step 7)
    config.yaml
    projects.yaml
AGENTGATEWAY_HOME/        # agentgateway daemon home (created in step 1)
  .agentgateway/
    config.yaml
    virtual-models/         # the 4 models live here
      claude-code/high.yaml
      claude-code/low.yaml
      claude-code/medium.yaml
      codex/default.yaml
    mcp-servers/            # MCP servers (created in step 4)
      ai-memory.yaml
      codegraph.yaml
      serena.yaml
      xerj.yaml
    mcp-clients/            # MCP clients (created in step 5)
      codex.yaml
      claude-code.yaml
```

---

## 1. Install agentgateway.dev

`agentgateway` is the agent-agnostic CLI + daemon that sits between your rigs
and the model providers. Install the binary, then start the daemon.

```bash
# 1. Download and install (Linux amd64)
curl -fsSL https://github.com/agentgateway/agentgateway/releases/latest/download/agentgateway-linux-amd64 -o /usr/local/bin/agentgateway
chmod +x /usr/local/bin/agentgateway

# 2. Verify
agentgateway --version
# Expected: agentgateway version <X.Y.Z>

# 3. Start the daemon (daemonises itself, listens on a local unix socket)
agentgateway daemon start
# Expected: "agentgateway daemon started" — the process stays in the background.

# 4. Check the daemon is alive
agentgateway status
# Expected: "agentgateway: running"
```

If you are on a non-Linux platform, use the matching binary name from the
GitHub releases page.

**Where config lives.** The daemon home is `$AGENTGATEWAY_HOME` (default
`~/.agentgateway`). All config files in this guide are relative to that home
unless noted.

---

## 2. Configure the 4 virtual models

A *virtual model* is a named profile that maps a provider + model + API key +
settings into a single addressable endpoint. The four models you asked for:

| virtual model | provider | model | purpose |
|---|---|---|---|
| `claude-code/high` | Anthropic | `claude-sonnet-4-20250514` or equivalent | the heavy-duty seat |
| `claude-code/medium` | Anthropic | `claude-haiku-3-5-20241022` or equivalent | volume seat |
| `claude-code/low` | Anthropic | (any cheap haiku or equivalent) | background / grind seat |
| `codex/default` | OpenAI | `o3-mini` or `gpt-4.1-mini` or equivalent | the codex seat |

### 2a. Create the virtual model files

Each virtual model is a YAML file in `$AGENTGATEWAY_HOME/virtual-models/`.

```bash
mkdir -p "$AGENTGATEWAY_HOME/virtual-models"

cat > "$AGENTGATEWAY_HOME/virtual-models/claude-code/high.yaml" <<'EOF'
name: claude-code/high
provider: anthropic
model: claude-sonnet-4-20250514
api_key_env: ANTHROPIC_API_KEY
defaults:
  timeout: 600
  max_turns: 50
  temperature: 0
EOF

cat > "$AGENTGATEWAY_HOME/virtual-models/claude-code/medium.yaml" <<'EOF'
name: claude-code/medium
provider: anthropic
model: claude-haiku-3-5-20241022
api_key_env: ANTHROPIC_API_KEY
defaults:
  timeout: 300
  max_turns: 30
  temperature: 0
EOF

cat > "$AGENTGATEWAY_HOME/virtual-models/claude-code/low.yaml" <<'EOF'
name: claude-code/low
provider: anthropic
model: claude-haiku-3-5-20241022
api_key_env: ANTHROPIC_API_KEY
defaults:
  timeout: 120
  max_turns: 15
  temperature: 0
EOF

cat > "$AGENTGATEWAY_HOME/virtual-models/codex/default.yaml" <<'EOF'
name: codex/default
provider: openai
model: o3-mini
api_key_env: OPENAI_API_KEY
defaults:
  timeout: 300
  max_turns: 30
  temperature: 0
EOF
```

### 2b. Restart the daemon so it picks up the new models

```bash
agentgateway daemon restart
sleep 2
agentgateway models list
# Expected: the four virtual models above appear, each with a reachable URL.
```

---

## 3. Login each client to its virtual model

The provider key must be available in the environment of the process that will
use it. Add to `~/.bashrc` (or `~/.zshrc`):

```bash
export ANTHROPIC_API_KEY="sk-ant-..."      # your Anthropic key
export OPENAI_API_KEY="sk-proj-..."        # your OpenAI key
```

Log in each tool so it can resolve the virtual model names:

```bash
# Claude Code — no extra login needed; it reads ANTHROPIC_API_KEY directly.
# Confirm it can reach the gateway:
claude --version
# Expected: claude-cli vXX

# Codex — same, reads OPENAI_API_KEY.
codex --version
# Expected: codex CLI vX.Y.Z
```

Verify each tool can see the virtual models it should use:

```bash
# If these tools expose a "list providers/models" command, run it.
# Otherwise: confirm the virtual-models directory content above is what the
# gateway served after `agentgateway models list` in step 2b.
```

---

## 4. Configure MCP servers on the agentgateway

The agentgateway can run MCP servers itself (stdio or streamable HTTP). The four
servers you asked for: **ai-memory**, **codegraph**, **serena**, **xerj**.

### 4a. Install each MCP server

```bash
# ai-memory — the project memory layer
curl -fsSL https://github.com/ai-memory/ai-memory/releases/latest/download/ai-memory-linux-amd64 -o /usr/local/bin/ai-memory
chmod +x /usr/local/bin/ai-memory

# codegraph — call-path / blast-radius index (CLI only)
curl -fsSL https://github.com/codegraph/codegraph/releases/latest/download/codegraph-linux-amd64 -o /usr/local/bin/codegraph
chmod +x /usr/local/bin/codegraph

# serena — LSP / symbol index for one project per repo
uv tool install serena-agent

# xerj — (replace with the actual xerj installer if it differs)
# e.g. `npm i -g xerj` or the release binary link
```

### 4b. Register the servers in agentgateway

Each MCP server gets a config file in `$AGENTGATEWAY_HOME/mcp-servers/`.
`transport` is `stdio` for local binaries and `http` for a URL.

```bash
mkdir -p "$AGENTGATEWAY_HOME/mcp-servers"

cat > "$AGENTGATEWAY_HOME/mcp-servers/ai-memory.yaml" <<'EOF'
name: ai-memory
transport: stdio
command: ai-memory
args: [server, --config, /home/dinhtrung/.config/ai-memory/config.yaml]
env:
  HOME: /home/dinhtrung
EOF

cat > "$AGENTGATEWAY_HOME/mcp-servers/codegraph.yaml" <<'EOF'
name: codegraph
transport: stdio
command: codegraph
args: [server, --project, /home/dinhtrung/develop/fleet-console]  # use your project path
env:
  HOME: /home/dinhtrung
EOF

cat > "$AGENTGATEWAY_HOME/mcp-servers/serena.yaml" <<'EOF'
name: serena
transport: stdio
command: serena
args: [project, index, /home/dinhtrung/develop/fleet-console]
env:
  HOME: /home/dinhtrung
EOF

cat > "$AGENTGATEWAY_HOME/mcp-servers/xerj.yaml" <<'EOF'
name: xerj
transport: stdio
command: xerj
args: [serve, --port, 4001]
env:
  HOME: /home/dinhtrung
EOF
```

Restart agentgateway so the servers register:

```bash
agentgateway daemon restart
sleep 2
agentgateway mcp list
# Expected: ai-memory, codegraph, serena, xerj all show as "running" or "available".
```

---

## 5. Configure MCP clients for codex and claude-code

The clients tell each tool which MCP servers to attach. The config lives next to
the tool, not in agentgateway.

### 5a. Codex MCP client

Create `~/.codex/mcp.json` (or wherever codex reads its MCP config — check
`codex config get mcp_servers`):

```bash
mkdir -p ~/.codex

cat > ~/.codex/mcp.json <<'EOF'
{
  "mcpServers": {
    "ai-memory": {
      "url": "http://localhost:3001/mcp"
    },
    "codegraph": {
      "command": "codegraph",
      "args": ["server", "--project", "/home/dinhtrung/develop/fleet-console"]
    },
    "serena": {
      "command": "serena",
      "args": ["project", "index", "/home/dinhtrung/develop/fleet-console"]
    },
    "xerj": {
      "url": "http://localhost:4001/mcp"
    }
  }
}
EOF
```

### 5b. Claude Code MCP client

Claude Code reads MCP config from `~/.claude/mcp.json`:

```bash
mkdir -p ~/.claude

cat > ~/.claude/mcp.json <<'EOF'
{
  "mcpServers": {
    "ai-memory": {
      "url": "http://localhost:3001/mcp"
    },
    "codegraph": {
      "command": "codegraph",
      "args": ["server", "--project", "/home/dinhtrung/develop/fleet-console"]
    },
    "serena": {
      "command": "serena",
      "args": ["project", "index", "/home/dinhtrung/develop/fleet-console"]
    },
    "xerj": {
      "url": "http://localhost:4001/mcp"
    }
  }
}
EOF
```

**Verify** both clients:

```bash
# Codex
codex mcp list   # (or the tool's equivalent)

# Claude Code
claude mcp list
# Expected: all four servers listed per client.
```

---

## 6. Initialise the rig on the example project

`rig` (OpenRig) owns the seats and the spec. In the project directory:

```bash
cd example-project

# 1. Write rig.yaml — a spec that references the virtual models from step 2
cat > rig.yaml <<'EOF'
version: "0.2"
name: example-project
summary: >
  Example agent fleet: 3 Claude Code seats (high / medium / low) + 1 codex seat.
culture_file: CULTURE.md

env:
  ANTHROPIC_API_KEY: "${ANTHROPIC_API_KEY}"
  OPENAI_API_KEY: "${OPENAI_API_KEY}"

pods:
  - id: coders
    label: Coding seats
    members:
      - id: claude-high
        agent_ref: "local:agents/development/implementer"
        runtime: omp
        model: claude-code/high
        effort: high
        cwd: "${PWD}"
      - id: claude-medium
        agent_ref: "local:agents/development/implementer"
        runtime: omp
        model: claude-code/medium
        effort: medium
        cwd: "${PWD}"
      - id: claude-low
        agent_ref: "local:agents/development/implementer"
        runtime: omp
        model: claude-code/low
        effort: low
        cwd: "${PWD}"
      - id: codex
        agent_ref: "local:agents/development/implementer"
        runtime: omp
        model: codex/default
        effort: medium
        cwd: "${PWD}"

culture_file: CULTURE.md
EOF
```

### 6a. Validate and launch the rig

```bash
# 1. Validate the spec against the rig schema
rig spec validate ./rig.yaml --json

# 2. Sync specs and bring the seats up
rig specs sync
rig up ./rig.yaml --non-interruptive --json
```

**Success checks:**
```bash
rig ps        # 4 seats listed, all "up"
rig log       # recent activity per seat
```

---

## 7. Declare the project for fleet-console

fleet-console owns the dispatch: which project maps to which rig, with a
per-project concurrency cap.

```bash
mkdir -p "$HOME/.config/fleet-console"

# 1. The registry: one project -> one rig, one cap
cat > "$HOME/.config/fleet-console/config.yaml" <<'EOF'
projects:
  - name: example-project
    repo: git@github.com:<your-org>/example-project.git
    rig: rig.yaml
    seats:
      - name: claude-high
        capacity: 1
      - name: claude-medium
        capacity: 2
      - name: claude-low
        capacity: 3
      - name: codex
        capacity: 1
    concurrency_cap: 4
    onhold: false
EOF
```

```bash
# 2. Ask the fleet-console CLI to apply the registry
fleet-console --config "$HOME/.config/fleet-console/config.yaml" --apply-config
# Expected: "created example-project" (or "unchanged example-project").
```

---

## 8. Create the Claude epic skill

fleet-console ships a skill directory for Claude. The skill is the template
Claude follows when it creates an epic.

### 8a. Skill structure

```bash
mkdir -p "$HOME/.claude/skills/create-epic"
```

### 8b. The skill file

```bash
cat > "$HOME/.claude/skills/create-epic/SKILL.md" <<'EOF'
---
name: create-epic
description: Create a project epic in fleet-console. Use when the user asks to start a new epic or when a request is too large to be one ticket.
---

# Create epic

1. Read the user's request and extract:
   - what the user wants (outcome),
   - the constraint set,
   - any success criteria.

2. Break the outcome into a **title** (one line, imperative, ≤ 72 chars) and a
   **body** (5–10 bullets, each ≤ 120 chars).

3. Draft the epic body as a fleet-console item:
   - `title` — from step 2,
   - `description` — the bullets + acceptance criteria,
   - `scope` — what is in, what is explicitly out,
   - `size` — XS/S/M/L/XL based on estimated effort,
   - `labels` — begin with the domain tag.

4. Write the prompt below into a file `epic-start-prompt.md` and deliver it to
   the user as the "what happens next" message.
EOF
```

---

## 9. The starting prompt on Claude

When the user asks to begin an epic, feed this prompt to Claude Code. It both
creates the epic and hands the user the next-step message.

```
Create an epic for the following request.

Request:
<USER-REQUEST-HERE>

Use the create-epic skill (read it first if the skill tool is available):
  skill: create-epic

Redirect plan:
1. Read the request.
2. Break it into a title (<=72 chars) and 5-10 bullets (<=120 chars each).
3. Draft the fleet-console item: title, description, scope, size, labels.
4. Write the starting prompt to epic-start-prompt.md and show it to the user.
5. Do NOT write any code yet.
```

The deliverable the user receives is the `epic-start-prompt.md` contents, which
they copy into `rig.yaml` or a new `project.yaml` for the rig to pick up later.

---

## 10. End-to-end verification

Run these in order after everything is configured. Every check must be green.

| # | Command | Expected |
|---|---|---|
| 1 | `agentgateway models list` | 4 virtual models visible |
| 2 | `agentgateway mcp list` | 4 MCP servers visible |
| 3 | `claude mcp list` | ai-memory, codegraph, serena, xerj listed |
| 4 | `codex mcp list` | ai-memory, codegraph, serena, xerj listed |
| 5 | `rig ps` (in the project dir) | 4 seats up |
| 6 | `fleet-console --config ... --print-config` | project registered |
| 7 | `fleet-console --config ... --tick` | run without error (dry or real) |

---

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `agentgateway: command not found` | binary not in PATH | install to `/usr/local/bin`, or add `~/.local/bin` to PATH |
| `agentgateway models list` empty | daemon not restarted | run `agentgateway daemon restart` |
| `rig up` fails with `model not found` | virtual model name typo | check `agentgateway models list` output matches rig.yaml exactly |
| MCP server shows `starting` forever | the command is missing / wrong env | check the `env:` block and run the command manually to see the error |
| `rig ps` shows seats stuck | daemon not connected | `rig daemon restart` then `rig ps` |
| `fleet-console --tick` refuses | dirty tree or no config | run `--print-config` first, then `--tick --json` for the exact gate |

---

## Quick reference — one-shot commands

```bash
# Install + start gateway
curl -fsSL https://github.com/agentgateway/agentgateway/releases/latest/download/agentgateway-linux-amd64 -o /usr/local/bin/agentgateway
chmod +x /usr/local/bin/agentgateway
agentgateway daemon start

# Full setup for one project (assuming /home/dinhtrung/develop/example-project)
cd /home/dinhtrung/develop/example-project
# ... (steps 1-7 as above) ...
rig spec validate ./rig.yaml && rig up ./rig.yaml --non-interruptive

# Verify
rig ps && fleet-console --config "$HOME/.config/fleet-console/config.yaml" --print-config
```
