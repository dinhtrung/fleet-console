# my-stack.md — the research / coding stack

The layering I use for agent-driven development: which tool owns which question, how each is installed, how each is
verified, and how a fleet of agents is driven without a model in the control path. Project names, hosts and paths
are removed on purpose — every `<repo>` is a placeholder for a checkout of your own.

Everything here is measured, not guessed. Numbers in this file are from a Linux box (Ubuntu 26.04, Node 24, Go 1.25,
uv/Python 3.13, tmux).

---

## 0. TL;DR — one owner per layer

| Layer | Owner |
|---|---|
| The hard problems | a premium coding agent (Claude Code) |
| Heavy, always-on work | a cheaper agent with a big context (pi.dev + DeepSeek) |
| Many seats → merge requests | OpenRig (`rig`) |
| Symbols / LSP / refactor | `serena` (one project per repo, pinned to the main checkout) |
| Prose and spec search | a local docs index (sqlite + embeddings) |
| Call paths / blast radius | `codegraph` (CLI only, one index on the main checkout) |
| Compressing tool output | `rtk` |
| Compressing prose into agent context | `caveman` |
| Project memory | `ai-memory` |
| Spec workflow | `openspec` |
| Forge access | `glab` / `gh` CLI — **not** an MCP server |

Removed on purpose (each one was paying context for a question another tool already answered): a code-graph MCP,
a codebase-memory MCP, a forge MCP in the agent runtimes, and a "context mode" plugin. Measured effect on a Claude
session: **~70k → ~23k tokens of tool schema**.

---

## 1. Principles

1. **One owner per layer.** Two tools answering one question double the context for one capability.
2. **Tool schema is a permanent cost**, paid every turn, not per use. Measure it with `tools/list`; never estimate.
3. **One secret, one source.** A token lives in the CLI's own store; other clients resolve it at launch.
4. **Measure with the tool's own status**, not with an exit code: `rtk history.db`, `ai-memory status`, the index's
   `status`, `rig ps`, `docker images`.
5. **Runtime by purpose.** Expensive model = the hard problems; cheap model = volume; orchestration = deterministic
   scripts, no LLM.

---

## 2. Layers — install and verify

Each row is the install command and the one command that proves it works. If the verify line is not green, the layer
is not installed, whatever `--version` says.

| Layer | Install | Verify |
|---|---|---|
| `rtk` | `curl -fsSL <release>/rtk-install.sh \| sh` | `rtk --version && rtk history --limit 5` |
| `serena` | `uv tool install serena-agent` | `serena project index <repo> && serena status` |
| language servers | `gopls` (Go), `typescript-language-server` (TS) | the server binary answers `--version` **with the seat's PATH** |
| `ai-memory` | release binary → `~/.local/lib/ai-memory`, user unit | `systemctl --user is-active ai-memory && ai-memory status` |
| docs index | local sqlite build over the docs tree | one query returns `path:start-end` for a known phrase |
| `codegraph` | release binary, `codegraph init <repo>` **once, on the main checkout** | `codegraph explore "<a symbol>" -p <repo>` |
| `openspec` | `npm i -g @fission-ai/openspec` | `openspec validate --all --strict` |
| `pi` | `npm i -g` the CLI, then `pi auth check` | one real turn that prints the model/provider it served |
| OpenRig | `npm i -g @openrig/cli` | `rig preflight && rig doctor && rig ps` |
| forge CLI | `glab auth login` / `gh auth login` | `glab api projects/<id>` — a token file is not a token |

Two rules that save hours:

- **An index lives on the main checkout only.** Worktrees share it; a second index is stale by lunchtime and quotes
  old line numbers with no warning.
- **A langauge server needs the seat's PATH.** A seat (an agent session) does not inherit your shell; if `go` or
  `node` is missing from its `PATH`, `serena` starts and answers nothing.

---

## 3. The automatic loop (the part worth copying)

Goal: write a spec and open tasks on the forge; the machine dispatches, reviews and merges without a human in the
loop, and **without a model in the control path**.

Four pieces:

| Piece | Where | Job |
|---|---|---|
| dispatcher | a script (`stdlib + forge CLI + rig`) | poll the forge, compute what is ready, cut a worktree, write a brief, create a queue row |
| timer | `systemd --user` (oneshot, `OnUnitInactiveSec`) | tick every 10 minutes |
| daemon unit | `systemd --user` | start the agent daemon at boot, guard if it is already up |
| rig | OpenRig spec | builders + one reviewer; the seats do the work |

No LLM in dispatch. The script owns no token: every call goes through the forge CLI.

### Ordering rules (the whole contract)

1. **The work unit is a task**, never an epic (an issue with children).
2. **Candidates**: open tasks whose parent epic is open, plus parentless tasks.
3. **Blocking**: a task waits while a *related* item with a **smaller** id is open. Forge links are symmetric, so
   checking both directions deadlocks the chain — check one.
4. **Order by id**, oldest first, across the project.
5. **Concurrency** = free seats, and N>1 only for genuinely independent tasks.
6. **A park label** (e.g. `onhold`) skips the item *and* every child of a parked epic, and only a human clears it.
7. **A marker label** (`status::ongoing`) has exactly one writer per transition: set at dispatch, cleared by the
   janitor or the reviewer.
8. **Housekeeping runs before the "no free seat" early return** — prune closed, reset stalled, reconcile the marker,
   re-nudge unclaimed rows.
9. **Idempotency covers rows the loop did not create**: scan live queue rows for the item id, because the state file
   only knows its own dispatches.

### Lifecycle of one task

```
task opens on the forge
  → dispatcher: worktree wt-<id> + branch feature/<id>-<slug> from origin/<default>
  → brief file → queue row → a seat claims it
  → builder: code + tests + draft merge request (Closes #<id>, Refs #<epic>)
  → handoff row → reviewer
  → reviewer: verdict; on ACCEPT + green head pipeline → undraft + merge --sha
  → the task closes itself (Closes) and the marker label is cleared
  → next tick: the predecessor is closed, so the next task is dispatched
```

### Three kinds of "idle", and why telling them apart is the whole job

1. **Nothing left to do** — healthy.
2. **Blocked by the rules** — the log names the blocking id; healthy until that id closes.
3. **Parked after a verdict** — a review row closed without handing work back, the branch still has an open merge
   request, and no row is alive. Every later task then reads `blocked by open #<id>` and the fleet looks idle
   forever. Diagnose it by reading the *review row's own fields* (`closureReason`), not just the row state:
   `done` hides both "approved" and "rejected, nothing returned".

The dispatcher must detect (3) itself — a log line, a note on the item, and the idle line carrying the parked ids —
because it is the failure nobody notices. It is also why an app that manages fleets needs **decisions as records**,
not notifications: see `docs/fleet-console.md`.

### Incidents worth designing against

| Symptom | Cause | Handling |
|---|---|---|
| Reviewer never merges; the row parks waiting for a human | the agent runtime refuses merges *by classification*, whatever the allow-list says | grant the seat an explicit permission mode, then relaunch (the mode applies to the next launch only) |
| A seat wakes after a quota reset but never claims its old row | unclaimed rows are not pushed again | the dispatcher re-nudges unclaimed rows on idle seats, with a cooldown |
| The dispatcher logs `abort: worktree exists` on every tick | a dead run left the worktree | reuse a worktree whose HEAD is the wanted branch; never delete one to unblock |
| A task stays open although its merge request merged | the body says `Refs #x` instead of `Closes #x` | detect by branch name against merged merge requests, warn, and ask the reviewer to close |
| `no task is ready` for hours | a verdict parked the chain | detect and surface, never auto-dispatch blindly |
| After a reboot nothing runs | no unit for the daemon | daemon unit enabled + `loginctl enable-linger` |
| Several seats stop in the same minute | quota is per account, not per seat | wait for the reset; adding seats buys no capacity |
| Two runs of the loop dispatch the same task | state file vs hand-made rows | a task lease plus a scan of live rows |

### Limits worth stating up front

- An unattended reviewer needs a permission posture that allows merging; that is a deliberate trade of guardrails
  for autonomy.
- Quota is per account: a fleet on one login stalls together. Only a second account/key makes it 24/7.
- Symmetric "related" links plus a smaller-id heuristic can block wrongly when the links are dirty.

---

## 4. Rebuilding this on a new box

```bash
# 0. base
sudo apt-get install -y tmux git curl jq ripgrep
# 1. node + go + uv, then the CLIs
npm i -g @fission-ai/openspec @openrig/cli @openai/codex
uv tool install serena-agent
# 2. the two indexes, once, on the main checkout
codegraph init <repo>
serena project index <repo>
# 3. memory + compressors (user units where they have one)
systemctl --user enable --now ai-memory
rtk init --show
# 4. keep the indexes fresh hourly while you work
crontab -e   # 07 * * * *  <your sync script>
# 5. the loop
systemctl --user enable --now agent-daemon.service dispatch.timer
```

Verify with §2's table, then one real task end-to-end. A green install with no end-to-end task proves nothing.

---

## 5. What is deliberately absent

- **No forge MCP.** The CLI does everything the MCP did, without 45k tokens of schema.
- **No second memory.** One memory service; two means neither is trusted.
- **No LLM in dispatch, ever.** Ordering is a contract, not a judgement call; a model in that path makes the fleet
  unreproducible.
- **No dashboard that counts tokens.** Cost is a report you ask for, not a screen you watch.
