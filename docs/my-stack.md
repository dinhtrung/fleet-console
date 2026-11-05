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

1. **The work unit is a task**, never an epic (an issue with children). **Label the container `type::epic` when
   you create it**: the label is the only fact that says "epic" before the first child is linked, and without it a
   tick in that window dispatches the epic body as a task (`forge.EpicLabel`).
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

#### The rig's own lifecycle, and the poll's cadence

Telling those three apart is a *reading*. The app also acts on the fourth thing an idle fleet costs: a rig with no
work still holds seats. So the control plane starts a project's rig when work arrives and stops it once the quiet
window has elapsed — `rig_lifecycle.enabled`, false by default so an upgraded box keeps today's behaviour.

```yaml
rig_lifecycle:
  enabled: true          # false: the app never starts or stops a rig
  idle_after: 10m        # how long a rig may sit with no work before it is stopped
  idle_poll_max: 10m     # the poll's ceiling while idle; refused when below tick_interval
projects:
  - key: thuocla-tracker
    lifecycle: off       # this project's rig stays warm; absent means "follow enabled"
```

Two properties make this safe to leave running. A rig is stopped only when nothing it owns needs a seat: no row in a
state that means work or waiting — and **a row blocked on a person does not hold the rig open**, because that rig
would idle for as long as the operator takes. A rig is started before its dispatch intent is written, so a row never
points at a seat that does not exist yet. Every start and stop writes one log line naming the rig, the action and the
reason, plus one `rig.started` or `rig.stopped` audit event; neither sends a phone message, because housekeeping is
neither work nor a decision. A failed `rig up` or `rig down` names the command and its error and records no success.

The poll backs off while the fleet is idle. `tick_interval` is the busy cadence and `idle_poll_max` the ceiling: each
quiet tick doubles the wait up to the ceiling, and the first busy tick returns it to `tick_interval`. The **latency
trade is the whole point** — a 10m ceiling means work that arrives just after a poll waits up to ten minutes to be
seen. Set `idle_poll_max` to what you are willing to wait, not to the longest you can tolerate. Each change of the
wait writes one log line, so a slow poll is distinguishable from a hung loop, and an unchanged wait writes nothing.

The exact lines to grep for:

```
rig-lifecycle: <rig> started — <reason>
rig-lifecycle: <rig> stopped — quiet <duration>
act-skip: <rig>: <command> failed: <error>          # a launch failed; its work is skipped this tick
mirror: cadence <wait> (quiet <n>)                  # the wait doubled
mirror: cadence <wait> reset: <the busy fact>       # the wait returned to tick_interval
```

So `journalctl --user -u fleet-console | grep 'rig-lifecycle:'` reads the lifecycle's whole history, and
`grep 'mirror: cadence'` reads the poll's. A broken forge read is *not* idleness — the wait stays at
`tick_interval` while the read is failing, because a rig that cannot see its work is not a rig without work.

The requirements live in `openspec/changes/rig-lifecycle/`; this section is orientation, not the contract.

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

## 4b. The fleet control plane (fleet-console)

| What | Value |
|---|---|
| binary | `fleet-console` — one static file, board embedded (`make build`) |
| unit | `fleet-console.service` (systemd --user; `%h/.local/bin/fleet-console`) |
| port | `127.0.0.1:8787` |
| config | `FLEET_CONSOLE_TOKEN` in a mode-600 env file; `FLEET_CONSOLE_DB` for the SQLite path |
| alerts | `TELEGRAM_BOT_TOKEN` + `TELEGRAM_CHAT_ID` (owner-supplied; never committed) |
| kill switch | `systemctl --user stop fleet-console && systemctl --user disable fleet-console` |
| dry run | `fleet-console --tick` — mirror + decide only; no forge, rig or git write |
| old timer | `gh-autodispatch.service` — stop and disable it before enabling the act loop |

### Turning the rig lifecycle off, and keeping one project warm

Two switches, at different scopes. To stop the app managing rigs at all, set `rig_lifecycle.enabled: false` — or delete
the block, which is the same thing and the default. To keep one project's rig up while the rest come and go, leave the
block enabled and give that project `lifecycle: off`; its rig then never starts or stops on its own. `--print-config`
shows the block and each project's effective value with the source it came from, so a project whose setting you cannot
explain is one command away from an answer.

Both are file edits. The running process reads the block once at boot, so after changing it:

```bash
systemctl --user restart fleet-console
```

A restart is safe: the board is a database and the mirror rebuilds from the forge on the next tick. Changing it while
the app is running does nothing, which is worth knowing before you conclude the setting has no effect.

## 5. What is deliberately absent

- **No forge MCP.** The CLI does everything the MCP did, without 45k tokens of schema.
- **No second memory.** One memory service; two means neither is trusted.
- **No LLM in dispatch, ever.** Ordering is a contract, not a judgement call; a model in that path makes the fleet
  unreproducible.
- **No dashboard that counts tokens.** Cost is a report you ask for, not a screen you watch.
