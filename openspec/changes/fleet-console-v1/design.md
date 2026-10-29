# Design — fleet-console v1

## 1. The problem, stated precisely

A single-repo dispatcher works until the second project exists. Then there is no cross-project view, state lives in
a JSON file with no transactions and no audit, and "the fleet is idle" needs four reads (`rig ps`, the tick log,
the queue rows, the forge) to discover that the real answer is *a verdict is parked, waiting for a human*.

This revision starts from a different premise than the 2026-10-27 draft: **OpenRig already owns work state**, so
the app must not build a second one. The app is the forge-aware planner and the surface, not the queue.

## 2. Invariants that survive unchanged

From `docs/my-stack.md` §3, still the contract:

1. **Work unit = task.** An epic (an item with children) is never dispatched.
2. **Candidates** are open tasks whose parent epic is open, or parentless tasks.
3. **Blocking** is one-directional per forge: GitLab uses `related_items` with a **smaller** iid because
   `relates_to` is symmetric and checking both ways deadlocks; GitHub uses native issue dependencies (`blocked_by`),
   which are already directional.
4. **Ordering** is ascending by item number within a project.
5. **Concurrency** is bounded by the free builders of that project's rig, capped by the project.
6. **`onhold`** on an epic skips the epic and every child; only a human clears it.
7. **The in-flight marker** has exactly one writer per transition (set at dispatch, cleared by the janitor or the
   reviewer).
8. **Housekeeping runs before the "no free builder" early return.**
9. **Idempotency covers rows the loop did not create** — a live row whose body names the item number blocks a
   second dispatch.
10. **A worktree is reused, never deleted to unblock.**

## 3. Ownership: rig vs app

| Fact | Owner | Read by the app | Written by the app |
|---|---|---|---|
| Queue row, its state, closure reason, transitions | rig | `rig queue list -A -a --json`, `queue show --full --json` | `queue create/claim/handoff/update/resolve` |
| Human decision, options, SLA, receipt | rig | `rig queue overdue`, `rig view show escalations` | `queue create --destination human@external --human-questions-file --tier --evidence-ref --verify` |
| Step progression between seats | rig | `rig workflow status/show/trace` | `workflow instantiate/project/route/abort` |
| Parked diagnosis | rig | `rig parked --rig`, `rig view show pickup` | none |
| Seat state, model, permissions | rig | `rig ps --nodes --full --json`, `rig seat status` | `rig seat set-permissions/set-model`, `launch/stop` |
| Account switch, usage | rig | `rig provider signals/accounts`, `rig usage` | `rig provider switch` |
| Forge items, links, PR/MR, checks | app | forge adapter | labels, comments, issue close, PR merge |
| Dispatch attempt: seat, branch, worktree, base sha, qitem id | app | SQLite `dispatch` | app |
| The verdict accepted for a merge | app | SQLite `verdict` | app (from the seat's note) |
| Who wrote what, when | app | SQLite `event` | app (redacted) |
| Rig spec for a project | app | generated file under `~/.openrig/specs/rigs/<rig>/` | app (`rig spec validate`, `specs sync`, `up`, `down`) |

The app may cache rig reads for the board (`snapshot`), but a cache is never the source of a decision: the act pass
re-reads the row it is about to touch.

## 4. Model

```
Project ──< Repo                      (1..n; worktrees cut from Repo.path)
        ├── Rig ──< Seat             (declared by the generated spec; capacity = free builders)
        └──< Item ──< Dispatch ──< Verdict
                          └── qitem id ──▶ rig queue row (not copied, referenced)
                          └── change request ──▶ checks (forge)
```

- **Project** — `key`, forge kind + project id, default branch, label vocabulary, gate commands, concurrency cap,
  enabled, `onhold`, rig spec template name.
- **Repo** — `key`, path, worktree root. Worktrees are `wt-<project>-<number>` so two repos never collide.
- **Rig** — generated from the template; the row names its builders and reviewer. The app stores the rig name and
  the template inputs, never a hand-edited spec.
- **Item** — a forge mirror: `number, kind (epic|task), state, title, labels, parent_number, blocked_by[], url`.
- **Dispatch** — one row per (item, attempt): seat, branch, worktree, base sha, brief path, qitem id, timestamps.
- **Verdict** — `dispatch_id`, `sha`, `accept|reject`, source note id, at. Only a verdict for the current head sha
  can gate a merge.
- **Event** — append-only audit of the app's own writes: actor, kind, subject, command, exit code, stdout hash.

## 5. Architecture

```
              ┌──────────────────────── fleet-console (one Go binary) ────────────────────────┐
  GitHub ───▶ │ mirror loop ──▶ SQLite ──▶ decide loop ──▶ act loop ──▶ forge / rig / git      │
  GitLab ───▶ │ (read, 15 s)   (owned+cache)  (pure, 1 min)  (serialized, audited)            │
  rig    ───▶ │                                        │                                      │
              │                       Fiber v3 REST + SSE ──▶ embedded board (go:embed)       │
              └───────────────────────────────────────────────────────────────────────────────┘
```

- **Mirror** (read-only, 15 s per project, jittered): forge items + links + PR/MR + checks; rig queue snapshot and
  seat snapshot; `git ls-remote` branch index. Writes mirror/cache tables only.
- **Decide** (pure, 60 s or on a mirror delta): the algorithm of §8 over the mirror snapshot, emitting intents
  (`dispatch`, `nudge`, `clear_marker`, `close_epic`, `raise_decision`). Replayable: same snapshot, same intents.
- **Act** (serialized, one at a time per project): executes intents through adapters, records one `event` per
  write, and is the only place that mutates the world. Global kill switch and per-project pause.

Why Go + Fiber v3: the deliverable is one static binary that can be copied to any fleet box (see
`stack-provisioning`), the loops are plain goroutines with `exec.CommandContext`, and Fiber v3 gives the REST + SSE
surface without pulling a second runtime. The rejected alternative (TanStack Start end-to-end) is recorded in §15.

## 6. Data (SQLite, WAL)

Owned tables:

```sql
CREATE TABLE project (
  id INTEGER PRIMARY KEY, key TEXT UNIQUE NOT NULL, name TEXT NOT NULL,
  forge_kind TEXT NOT NULL,                -- github | gitlab
  forge_project TEXT NOT NULL,             -- "dinhtrung/fleet-console"
  default_branch TEXT NOT NULL DEFAULT 'main',
  rig TEXT NOT NULL, rig_template TEXT NOT NULL,
  concurrency_cap INTEGER NOT NULL DEFAULT 2,
  enabled INTEGER NOT NULL DEFAULT 1, onhold INTEGER NOT NULL DEFAULT 0,
  gate_cmd TEXT, labels_json TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE repo (
  id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL REFERENCES project(id),
  key TEXT NOT NULL, path TEXT NOT NULL, worktree_root TEXT NOT NULL,
  UNIQUE(project_id, key)
);
CREATE TABLE item (                        -- forge mirror, one row per item
  id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL, number INTEGER NOT NULL,
  kind TEXT NOT NULL, state TEXT NOT NULL, title TEXT NOT NULL,
  labels TEXT NOT NULL DEFAULT '', parent_number INTEGER,
  blocked_by_json TEXT NOT NULL DEFAULT '[]', url TEXT NOT NULL,
  updated_at TEXT NOT NULL, UNIQUE(project_id, number)
);
CREATE TABLE dispatch (
  id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL, item_number INTEGER NOT NULL,
  attempt INTEGER NOT NULL, seat TEXT NOT NULL, branch TEXT NOT NULL,
  worktree TEXT NOT NULL, base_sha TEXT NOT NULL, qitem_id TEXT,
  brief_path TEXT, created_at TEXT NOT NULL, ended_at TEXT,
  end_reason TEXT,                         -- merged | rejected | stalled | superseded | abandoned
  UNIQUE(project_id, item_number, attempt)
);
CREATE TABLE verdict (
  id INTEGER PRIMARY KEY, dispatch_id INTEGER NOT NULL REFERENCES dispatch(id),
  sha TEXT NOT NULL, decision TEXT NOT NULL,  -- accept | reject
  source TEXT NOT NULL, at TEXT NOT NULL
);
CREATE TABLE event (
  id INTEGER PRIMARY KEY, at TEXT NOT NULL, actor TEXT NOT NULL, kind TEXT NOT NULL,
  subject TEXT NOT NULL, detail TEXT NOT NULL, exit_code INTEGER, stdout_hash TEXT
);
CREATE TABLE snapshot (                    -- cache of rig reads, never a decision source
  project_id INTEGER NOT NULL, kind TEXT NOT NULL, payload_json TEXT NOT NULL,
  synced_at TEXT NOT NULL, PRIMARY KEY (project_id, kind)
);
CREATE TABLE mirror_meta (
  project_id INTEGER PRIMARY KEY, last_forge_at TEXT, last_rig_at TEXT, etag TEXT
);
```

No `queue_row`, `decision`, `lease` or `task-state` table exists. "Blocked by", "free builders" and "in flight" are
derived from the snapshot and the owned tables.

## 7. The rig contract

Every rig command the app uses goes through one adapter function that returns a domain type and never a raw map:

- `RigVersion()`, `QueueList()`, `QueueShow(id)`, `QueueCreate(...)`, `QueueUpdate(...)`, `QueueHandoff(...)`,
  `QueueResolve(...)`, `Seats()`, `Parked(rig)`, `ProviderSignals()`, `SpecValidate()`, `Up()`, `Down()`, `Grow()`,
  `Launch()`, `Send()`.
- Decoding is strict about **required** facts: a missing `pickup_state`, `destination_session`, `state` or seat
  identity is an error, never a zero value. Unknown keys are recorded (once per key per version) but do not fail.
- A `runtime-compat` check at boot and on every upgrade: `rig --version` at or above the floor, plus one real read
  of `queue list` and `ps --nodes`. A failure refuses to start the act loop (the mirror and board still run) and
  shows the reason.
- Fixtures: `internal/rig/testdata/<version>/<command>.json` captured from a real box. Upgrading the rig floor means
  capturing new fixtures and diffing them; the diff is the migration list.

## 8. Forge adapters

One normalized model, two implementations:

- **Item**: number, kind (epic if it has children), state, title, body, labels, parent number, blocked-by numbers,
  url.
- **Change request**: number, head sha, draft, mergeable, body, closes-item number, checks (name, status,
  conclusion, allow_failure).
- **Writes**: set/clear marker label, comment, close item, merge change request at a sha.

GitHub (`gh api`): issues + sub-issues for the epic relation; issue dependencies for `blocked_by`; PRs, check runs
and commit statuses; `gh pr merge --squash --match-head-commit <sha>`. GitLab (`glab api`): issues + `related_items`
(smaller-iid rule), MRs, head pipeline jobs with `allow_failure`, `glab mr merge --sha`.

The adapter is chosen per project; the decide loop sees only the normalized model. A project is never a mix of the
two.

## 9. The decide algorithm (per project, ascending number)

```
for each project where enabled and not onhold:
  snapshot = mirror(project)
  housekeeping(project):                       # ALWAYS, before any early return
    prune dispatches whose item is closed
    reset dispatches stalled > N min with no open CR (attempts += 1, give up at 3)
    reconcile marker label (closed item, or no live rig row ⇒ clear)
    re-nudge unclaimed rows > 15 min on an idle seat (cooldown 30 min)
    detect parked: dispatch ended, CR open, no live row ⇒ decision(parked)
    detect marker leak: CR merged, item still open ⇒ decision(marker_leak)
    close epics whose children are all closed (comment listing them)
  free = builders(rig) - busy(snapshot.rows)
  if free == 0: return
  ready = []
  for t in open items, number asc:
     skip if t is an epic
     skip if onhold(t) or onhold(parent(t))
     skip if parent(t) exists and parent(t) is not an open epic
     skip if t already dispatched or named in a live row body
     skip if a branch feature/<number>-* exists on origin and has no merged CR
     skip if any blocked_by item is open            # GitHub: native; GitLab: smaller related iid
     ready.append(t); if len(ready) == min(free, cap): break
  for t in ready: intent(dispatch, t, next free seat)
```

## 10. Decisions live in the rig

The app does not store decisions. It creates a rig queue row addressed to a human with the question, options and
evidence, and reads the answer back:

```
rig queue create --destination <human>@external --human-intent decision \
  --summary "<project> #<n>: <the question>" --body-file <brief> \
  --human-questions-file <questions.json> --tier human-gate \
  --evidence-ref <artifact path> --verify
```

- SLA: the rig's tier deadline (`queue overdue`) plus the app's own escalation timer.
- Clearing: `rig queue resolve <qid> --decision "..."` — the answer, not a chat message.
- Escalation: on an overdue row the app sends one Telegram message (edit/replace, never repeat) with project, item,
  question and a board deep link. Telegram is the app's channel; the rig's gateway speaks Slack.
- Kinds remain: `parked`, `red_gate`, `marker_leak`, `product_question`, `quota` — each maps to one question row.

## 11. The merge watchdog (the only merge path)

The reviewer keeps the judgement; the console keeps the button. Merge only when all four facts hold, re-read in the
act pass, never from the snapshot:

1. an `ACCEPT <sha>` verdict exists for this dispatch,
2. that sha is the change request's current head,
3. the checks for that head are green (allow-failure respected; a docs-only CR with no checks counts as green),
4. it is mergeable and its body contains the closing keyword for the item (`Closes #<n>`; GitHub accepts
   `Closes #n`, GitLab `Closes #<iid>`).

Anything unknown refuses and raises a `red_gate` decision naming the missing fact. Merge is
`gh pr merge <n> --squash --match-head-commit <sha>` (or `glab mr merge <iid> --sha <sha> --yes`), then the item is
verified closed and the next item unblocked; if the merge did not close it, the app closes it and records why.

## 12. Rig lifecycle

A project row renders a spec from `templates/rig.yaml.tmpl`:

- inputs: rig name, one absolute `cwd` per seat, runtime + model per seat, account/env block, builder count,
  reviewer count, guidance file paths, culture file.
- path: `~/.openrig/specs/rigs/<rig>/rig.yaml` plus `CULTURE.md`, `guidance/`, and the `agents` symlink.
- sequence: `rig spec validate --json` → `rig specs sync` → `rig up <rig> --plan --json` → `rig up <rig>
  --non-interruptive --json` → verify every seat is `running`.
- growth: `rig grow <rigId> <seat> --pod dev --cwd <path>` then `rig export <rig> -o <spec>` so the spec matches
  the running topology; the record in SQLite keeps only the inputs.
- removal: `rig down <rig> --delete` and archive the spec directory (never `rig archive`, which keeps the name).
- credentials: per-seat agent dirs are seeded before launch (`state/omp/<seat>/agent/config.yml` with the provider
  key, `mcp.json` with the project's serena/codegraph entries); the app never prints them.
- one or more rigs per project. The default is one; a project that needs more builder slots lists them in order
  (§17 D5) and each rig stays bound to that project's checkout. Sharing one rig across projects is rejected in §15.

## 13. UI — shadcn/ui on sage

- **Component library**: shadcn/ui, generated by `shadcn@latest` into `src/components/ui`, built on its Base UI
  primitives, styled only through CSS-variable tokens. `components.json` is committed.
- **Appearance**: one, `sage`. The `:root` block carries the sage tokens; `[data-theme]` variants are gone; the
  appearance registry exports a single entry and the switcher is removed from the shell.
- **Token bridge**: shadcn's semantic variables (`--background`, `--foreground`, `--primary`, `--border`, `--ring`,
  `--radius`) are mapped onto the sage palette roles in `src/index.css`, so a shadcn component inherits the board's
  ground/panel/rail/ink/rule roles instead of shipping its own colours.
- **Density and ruled layout survive**: no elevation, hairline rules, fixed columns, one-row-per-record. A shadcn
  component that cannot meet that is restyled at the token level, not overridden per screen.
- **Screens**: board, decisions, seats, task drawer, settings — unchanged in purpose.

## 14. API

`GET /api/board`, `GET /api/items/{n}`, `GET /api/decisions`, `POST /api/decisions/{id}/answer`,
`POST /api/items/{n}/dispatch`, `POST /api/rows/{qid}/resolve|handoff|close`, `POST /api/seats/{seat}/stop|launch`,
`POST /api/projects/{key}/pause|resume`, `GET /api/events`, `GET /api/stream` (SSE), `GET /api/health`,
`GET /api/compat`. Every POST needs a bearer token, is idempotent by `Idempotency-Key`, and returns the `event_id`
it wrote. The board gets a session cookie, never a forge token.

## 15. Decisions and rejected alternatives

1. **rig owns work state** (rejected: keep `queue_row`/`decision`/`lease` tables). Reason: two sources of truth
   for one row; the rig already has transactional handoff and an append-only transition log. Cost accepted: the
   app's board depends on rig CLI shapes, which is why `runtime-compat` exists.
2. **Go 1.25 + Fiber v3 + one static binary** (rejected: pure TypeScript/TanStack Start). Reason: the artifact
   must be copyable to a fleet box as one file; a Node SEA build is ~110 MB and experimental, a Docker image is
   ~200 MB and still needs the host's rig/tmux. TS kept the type-sharing win but lost the packaging requirement.
   Fiber v3 over `net/http`: the route/middleware/SSE surface is the app's whole API and Fiber's group/middleware
   model keeps the 12 handlers readable; the stdlib remains a one-file change if Fiber's v3 API moves.
3. **SQLite via `modernc.org/sqlite`** (rejected: `mattn/go-sqlite3`). Reason: `CGO_ENABLED=0` is what makes the
   single static binary real; one writer plus WAL is the whole concurrency story.
4. **`rig` and `gh`/`glab` as host tools** (rejected: reimplement the daemon API and forge HTTP directly). Reason:
   the CLIs own authentication and the rig's own semantics; a library would duplicate two release trains. Accepted
   cost: the binary is not hermetic, and the packaging spec says so.
5. **Rig lifecycle generated from the registry** (rejected: one shared rig for all projects, or a hand-authored rig
   per project). Reason: a seat's cwd, MCP configuration, culture and guidance are launch-time and per-repo; one
   reviewer per rig serializes reviews; `rig down` must have a per-project blast radius. Generated per project is
   the same isolation with none of the manual work.
6. **Two forge adapters, GitHub first** (rejected: GitLab-only). Reason: this repository and its fleet live on
   GitHub, the existing fleets live on GitLab, and the normalized model is small (item, CR, checks).
7. **Runtime compat probe + fixtures** (rejected: pin an exact rig version and assume shape). Reason: boxes in this
   estate already run 0.7.0 and 0.7.4; a pinned binary would refuse to work on the box that has the fleet.
8. **Task order: rules and oracle before the board** (rejected: board-first P0). Reason: a board over an unproven
   planner is the failure this project exists to remove; the golden plan comparison is the only evidence the port
   is faithful.
9. **shadcn/ui + sage only** (rejected: keep the inherited component system and five appearances). Reason: the
   owner chose one library and one appearance; five appearances are five token sets to keep contrast-correct for a
   single-operator board.

## 16. Phases

| Phase | Ships | Proof |
|---|---|---|
| P0 | module, Fiber v3 skeleton, SQLite, compat probe, rule port + oracle harness | `go test ./...` green; plan matches the dispatcher's on recorded ticks |
| P1 | forge adapters (GitHub, then GitLab), mirror, decide dry-run | a week of dry-run intents; parked/red-gate/marker-leak fire on fixtures |
| P2 | rig lifecycle + act loop (dispatch, rows, merge watchdog) | a real issue dispatched, reviewed and merged by the watchdog on four facts |
| P3 | web foundation (shadcn + sage) + board, decisions, drawer | screenshot of two projects, one paused, one parked, on the sage tokens |
| P4 | alerts, quota, second project, cutover | one Telegram escalation; two projects dispatching with isolated rigs |

## 17. Operating decisions (2026-10-29)

Six decisions taken after the first real install (three projects, two rigs, one live dispatch loop). Each one is a gap
the install exposed; the requirement that carries it is named beside it.

| # | Decision | Rejected because | Carried by |
|---|---|---|---|
| D1 | Exactly one instance may tick a database. The process takes a `flock` on `<db>.lock`; a second instance exits with `another instance owns <db>` instead of ticking. | Without a lease, a manual `--apply` beside the running service dispatches the same item twice — the same race the old dispatchers had. | single-writer |
| D2 | A verdict whose sha is no longer the head is a first-class state (`verdict stale`), and the act loop raises a decision to the reviewer asking for a re-review of the current head. | A `parked` row tells nobody; the ACCEPT silently rots every time a builder merges `main` into its branch. Observed twice on 2026-10-29. | merge-watchdog |
| D3 | A project missing from `config.yaml` gets `enabled = 0` on apply. `--prune` is the only thing that deletes rows. | The act loop iterates the DB, so a project removed from the file kept receiving dispatches forever. | project-registry |
| D4 | The rig wins every conflict with the app's `dispatch`/`verdict` tables. Each tick reconciles: terminal row with an open dispatch → `superseded`; merged CR with an open dispatch → `merged`; open dispatch with no row for one tick → `abandoned`. | Two records of "who is working on what" with no arbitration leaves items `building` forever after a hand-closed row. | dispatch |
| D5 | A project has **one or more** rigs, each launched for that project's checkout (`rig up` from the project folder, seats' cwd inside it). The registry lists them as tiers; free builders are summed across the active tier in listed order, and every rig keeps its own reviewer. Sharing a rig across projects stays rejected (§15.5). | One shared rig puts two repos' work behind a single reviewer and mixes MCP/codegraph indexes; a single-rig-per-project cap cannot scale a busy project without a second rig. | project-registry, rig-lifecycle, dispatch |
| D6 | Dispatch is two-phase: write the dispatch row `pending`, create the queue row, then stamp `qitem_id`. On boot, a `pending` dispatch with no queue row is adopted by branch/summary, or marked `abandoned`. | The current order (worktree → row → record) leaves a live row with no record after a crash, and the next tick creates a second row for the same item. | dispatch |

D5 reverses the earlier "one rig per project" framing in §12: the rule is one *project* per rig, not one rig per project.

D5a splits it: a rig serves one project; a project may name several, as `rigs: [{name, tier, spill}]`, with `rig: name`
as the one-element form. D5b is the scheduling rule: inside a tier rigs are filled in listed order, and a tier boundary
is crossed only by its trigger (`on-full` for overflow, `on-unavailable` for a standby). Health — a seat holding a row
with no output for `stall_after` — is what makes a rig unavailable, never a load number. The standby case that prompted
it: tsim runs Claude as tier 1 and omp as tier 2 `on-unavailable`, so omp's quota is spent only when Claude cannot work.
The deployment that prompted it — `mnpserver2` pointed at the `tsim-fleet-claude` rig — was the violation.
