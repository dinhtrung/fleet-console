# Design — fleet-console v1

## 1. The problem, stated precisely

`dispatcher.py` works and its rules are right, but it is bound to one repo and one rig, and its memory is a
JSON file. Four projects on four repos would mean four copies of the script, four systemd timers, four logs to read,
four state files to keep consistent, and no place that answers "what is the fleet doing across all of them — and what
is waiting for me?".

Today the answer to "why is the fleet idle?" takes four reads (`rig ps`, the tick log, the queue rows, the forge) and
a human who remembers that `nothing: no task is ready` can mean *a verdict is parked* — and a task that sat blocked for
two hours is that failure. That is the gap this app closes:

- **one place** for projects, repos, rigs and seats;
- **one durable state** for dispatch, leases, verdicts and merges;
- **one board** that names the blocking reason and offers the action;
- **one audit trail** for every write the fleet made.

Non-goals, deliberately: it is not a model in the loop (dispatch is deterministic — `docs/my-stack.md` §3, no LLM in the
dispatch step), it does not replace `rig` (it drives it), it does not replace the forge, and it does not write
code. It also does not merge on a builder's word: the four facts of §9 are the only path to a merge.

## 2. What the existing loop must keep

Straight from `docs/my-stack.md` §3, these are invariants, not implementation details:

1. **Work unit = task.** An epic (an issue with children) is never dispatched.
2. **Candidates** are open tasks whose parent epic is open, or parentless tasks.
3. **Blocking** is `related_items` with a **smaller iid** still open — one direction only, because GitLab's
   `relates_to` is symmetric and checking both deadlocks the chain.
4. **Ordering** is by iid within a project, oldest epic first.
5. **Concurrency** is bounded by free builders of that project's rig, and N>1 only for genuinely independent tasks.
6. **`onhold`** on an epic skips the epic and every child; only a human clears it.
7. **`status::ongoing`** has exactly one writer per transition (set at dispatch, cleared by the janitor or the
   reviewer) — no racing.
8. **Housekeeping runs before the "no free builder" early return**: prune closed, reset stalled, reconcile the
   marker label, re-nudge unclaimed rows.
9. **Idempotency covers rows the loop did not create** — scan live row bodies for `#<iid>`, because the state file
   only knows its own dispatches.
10. **Reuse a worktree** whose HEAD is the wanted branch (keep uncommitted work); never delete one to unblock.

## 3. Model

```
Project ──< Repo            (1..n; the worktrees are cut from Repo.path)
        └─< Rig ──< Seat   (builders bound + one reviewer; capacity = free builders)
        └─< Task ──< Dispatch ──< Lease
                    └─ QueueRow (mirror of rig's row)
                    └─ Verdict ──< Merge
                    └─ Decision  (a human obligation: parked row, red gate, product question)
```

- **Project** — `key` (`app`), display name, forge project id, label vocabulary, default branch, gate commands,
  enabled, concurrency cap, `onhold`.
- **Repo** — a project may own several (`app`, `docs`, `infra`). A task names the repo it lands in; worktrees are
  `~/develop/wt-<repo>-<iid>` so two repos never collide on an iid.
- **Rig/Seat** — which rig serves the project, which seats build, which reviews, each seat's runtime (pinned per
  spec) and its *observed* model (probe, never guess).
- **Task** — a mirror of a forge item: `iid, repo, type, state, labels, parent_iid, updated_at, title`.
- **Dispatch** — one row per (task, attempt): seat, branch, worktree, base sha, brief path, qitem id, timestamps.
- **QueueRow** — a mirror of `rig queue show`, refreshed each tick: `pickup.state`, `destinationSession`,
  `closureReason`; the app writes only through audited actions.
- **Decision** — the thing this app exists for: a blocking obligation with an owner (`human`), a question,
  options, a deadline, and the action that clears it.
- **Event** — append-only audit: who (human/loop/seat), what, on what, before/after, and the command's output hash.

## 4. Architecture

```
              ┌──────────────────────────── fleet-console (one Go binary) ───────────────────────────┐
  GitLab ───▶ │ mirror loop  ──▶ SQLite ──▶ decide loop ──▶ act loop ──▶ rig / git / forge adapters    │
  rig    ───▶ │ (read, 15 s)      (durable)   (pure, 1 min)  (audited)                                │
              │                                    │                                                  │
              │                          REST + SSE API ──▶ web board (static, embedded)               │
              └───────────────────────────────────────────────────────────────────────────────────────┘
```

Three loops, one writer:

- **Mirror** (read-only, every 15 s per project, jittered): forge items + related links + MRs + pipelines; rig
  `queue list -A -a --json`, `ps --nodes --json`; git branches on origin. Writes only the mirror tables.
- **Decide** (pure, every 60 s or on a mirror delta): runs the algorithm of §6 against the mirror and emits
  *intents* (`dispatch(task, seat)`, `nudge(row)`, `clear_marker(task)`, `close_epic(x)`, `raise_decision(...)`).
  Pure means replayable: given the same mirror, the same intents, so a bug is reproducible offline in a test.
- **Act** (serialized, one at a time per project): executes intents through adapters, records the command and its
  exit in `events`, and is the only place that mutates the world. Global kill switch and per-project pause.

Adapters are thin and swappable: `Forge` (GitLab REST via `glab api` first, GraphQL for the item list),
`Rig` (`rig ... --json`, or the daemon HTTP API with `OPENRIG_AUTH_BEARER_TOKEN` when it is up: fewer subprocesses),
`Git` (worktree add/reuse, fetch, branch checks), `Gates` (pipeline status + job list, never the badge).

Why Go + SQLite: it matches the repo's stack and the box (single static binary, a `systemd --user` unit, no new
service to keep alive), it gives real transactions for leases, and it can serve the embedded web build from one file.
The dispatcher's rules are already proven in Python; port them as tests, not as a rewrite (see tasks 2.1).

## 5. Data (SQLite, WAL)

```sql
CREATE TABLE project (
  id INTEGER PRIMARY KEY, key TEXT UNIQUE NOT NULL, name TEXT NOT NULL,
  forge_project TEXT NOT NULL,            -- "acme/example-app"
  default_branch TEXT NOT NULL DEFAULT 'main',
  rig TEXT, concurrency_cap INTEGER NOT NULL DEFAULT 2,
  enabled INTEGER NOT NULL DEFAULT 1, onhold INTEGER NOT NULL DEFAULT 0,
  gate_cmd TEXT,                          -- e.g. "go test ./... && npm run typecheck"
  labels_json TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE repo (
  id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL REFERENCES project(id),
  key TEXT NOT NULL, path TEXT NOT NULL,  -- /srv/dev/example-app
  worktree_root TEXT NOT NULL, UNIQUE(project_id, key)
);
CREATE TABLE task (
  id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL REFERENCES project(id),
  iid INTEGER NOT NULL, repo_key TEXT NOT NULL, type TEXT NOT NULL,  -- task | epic
  state TEXT NOT NULL, title TEXT NOT NULL, parent_iid INTEGER,
  labels TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL,
  UNIQUE(project_id, iid)
);
CREATE TABLE dispatch (
  id INTEGER PRIMARY KEY, task_id INTEGER NOT NULL REFERENCES task(id),
  attempt INTEGER NOT NULL, seat TEXT NOT NULL, rig TEXT NOT NULL,
  branch TEXT NOT NULL, worktree TEXT NOT NULL, base_sha TEXT NOT NULL,
  qitem_id TEXT, brief_path TEXT, created_at TEXT NOT NULL, ended_at TEXT,
  end_reason TEXT,                        -- merged | rejected | stalled | superseded | abandoned
  UNIQUE(task_id, attempt)
);
CREATE TABLE lease (                      -- one live claim per task, TTL 30 min, renewed by the loop
  task_id INTEGER PRIMARY KEY REFERENCES task(id),
  owner TEXT NOT NULL,                    -- "loop@fleet-console"
  expires_at TEXT NOT NULL
);
CREATE TABLE decision (
  id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL, task_iid INTEGER,
  kind TEXT NOT NULL,                     -- parked | red_gate | product_question | quota | marker_leak
  question TEXT NOT NULL, options_json TEXT NOT NULL, answer TEXT,
  created_at TEXT NOT NULL, answered_at TEXT, answered_by TEXT, sla_at TEXT
);
CREATE TABLE event (
  id INTEGER PRIMARY KEY, at TEXT NOT NULL, actor TEXT NOT NULL,
  kind TEXT NOT NULL, subject TEXT NOT NULL, detail TEXT NOT NULL, exit_code INTEGER
);
CREATE TABLE mirror_meta (project_id INTEGER PRIMARY KEY, last_forge_at TEXT, last_rig_at TEXT, etag TEXT);
```

Derived, not stored: "free builders" (rig snapshot minus live rows), "blocked by" (task graph computed in the decide
pass), "in flight" (dispatch without `ended_at`).

## 6. The decide algorithm (per project, iid order)

```
for each project where enabled and not onhold:
  snapshot = mirror(project)                      # items, links, rows, branches, pipelines
  housekeeping(project):                          # ALWAYS, before any early return
    prune dispatches whose task is closed
    reset dispatches stalled > N min with no MR (attempts += 1, give up at 3)
    reconcile marker label (closed task or no live row ⇒ clear status::ongoing)
    re-nudge rows unclaimed > 15 min on an idle seat (cooldown 30 min)
    detect parked: dispatch ended, branch has an open MR, no live row ⇒ decision(parked)
    detect marker leak: MR merged but task open ⇒ decision(marker_leak)
    close epics whose children are all closed (comment listing them)
  free = builders(rig) - busy(snapshot.rows)
  if free == 0: return
  ready = []
  for t in open tasks of project, iid asc:
     skip if t is epic
     skip if onhold(t) or onhold(parent(t))
     skip if parent(t) exists and parent(t) is not an open epic
     skip if t in dispatched or in a live row body
     skip if a branch feature/<iid>-* exists on origin and no merged MR for it
     skip if any related item with a SMALLER iid is open     # one direction only
     ready.append(t); if len(ready) == min(free, cap): break
  for t in ready: intent(dispatch, t, next free seat)         # one worktree per (repo, iid)
```

`dispatch` = `git fetch` → base sha → worktree add-or-reuse → brief from template + task body + project gates →
`rig queue create --source human@kernel --destination <seat> --body-file <brief>` → set `status::ongoing` → lease.
Every step idempotent: an existing worktree on the right branch is reused, an existing row for the iid aborts the
dispatch, a second loop instance is excluded by the project lease.

## 7. Decisions — the part that was missing

`decision` is a first-class object, because "the fleet is idle" is usually "one question is unanswered".

Kinds and how they clear:

| kind | raised when | options the board offers |
|---|---|---|
| `parked` | dispatch ended, MR still open, no live row | *hand a fix row to the branch's builder* · *re-dispatch* · *close as superseded* |
| `red_gate` | head pipeline red, or a gate command failed | *open the failing job* · *retry the job* · *accept the flake* (records job ids) |
| `marker_leak` | MR merged, task still open | *close the task now* (with the merged sha) |
| `product_question` | a seat's row closed `blockedOn: human` | free text answer → posted as the decision on the row, then resolved |
| `quota` | every seat on an account stops within a minute | *pause the project until the reset* · *switch account* |

Each decision has an SLA (parked: 30 min, red gate: 2 h). Missing it escalates to Telegram with the project, the
task, the question and a deep link into the board — that is the alert that was silent for two hours today.

## 8. Seats, capacity and quota

- Capacity is read from the rig, not configured: `ps --nodes --json` (seats, `sessionStatus`, `agentActivity`) minus
  rows `working|claimed|in-progress`. A seat that is `needs_input` on `permission_prompt` counts as busy **and**
  raises a `product_question` — a fleet parked on a menu is not idle, and no tick can fix it.
- Quota: when ≥2 seats of one rig go `stalled-after-claim` within 5 minutes, raise one `quota` decision for the
  account rather than one per seat, and stop dispatching to that rig until the reset. Never add seats for capacity:
  quota is per account (docs/my-stack.md §3 limits).
- Permission posture is shown per seat (`full_bypass`, `acceptEdits`, auto) because a reviewer that believes it
  cannot merge is a documented failure mode; the board shows the mode and the last launch time.

## 9. The merge watchdog (the only merge path)

The reviewer keeps the judgement; the console keeps the button. A merge intent is emitted only when all four facts
hold, read fresh in the act pass — never from a cached mirror:

1. an `ACCEPT <sha>` verdict exists for this dispatch (decision/verdict table),
2. that sha is the MR's current head,
3. the MR's **pipeline** for that head is green (job list, `allow_failure` respected; docs-only with no pipeline
   counts as green),
4. `detailed_merge_status == mergeable` and the body contains `Closes #<iid>`.

Anything unknown refuses and raises a `red_gate` decision with the missing fact named. Merge is
`glab mr merge <iid> --sha <sha> --yes`, then the task is verified closed and the next item unblocked; if the merge
commit does not close the task, complete it and record why.

## 10. UI

One board, dense, dark-friendly, keyboard-first. Four screens plus a drawer.

```
┌ fleet-console ──────────────────────────────────────────────────────────────────────────────┐
│ ● app   ▶ 3 building · 1 review · 2 waiting on you        ● docs  ⏸ paused (quota)         │
├─────────────────────────────────────────────────────────────────────────────────────────────┤
│ app · main  1a2b3c4         ready 4   inflight 3   waiting-you 2   blocked 7   merged 12/h │
├──────────┬──────────────────────────────────────────────────────────────────────────────────┤
│ PROJECTS │ TASK                                   STATE      SEAT      AGE    WHY BLOCKED     │
│ app  12 │ #51 connection-pool store             building   build-1   24m    —               │
│ docs   4 │ #53 connection-pool form (MNP)        queued     —         3m     waits #52      │
│ infra  0 │ #42 archive timestamp-seed            waiting    —         2h     ⚠ parked: MR    │
│          │                                            on you                  !17 green,    │
│          │                                                                    no verdict      │
│          │ #55 connection-pool archive           blocked    —         2h     waits #54      │
├──────────┴──────────────────────────────────────────────────────────────────────────────────┤
│ ▲ 2 decisions need you   [ open the oldest ▸ ]        ⏱ oldest 2h 04m   SLA 30m  ⚠ breaching │
└─────────────────────────────────────────────────────────────────────────────────────────────┘
```

- **Board** (above): per project, the counts that matter, then tasks ordered iid, each with the *reason* it is not
  moving (waits #x / parked / red gate) — the "three kinds of idle" from §3.8 rendered instead of logged.
- **Decisions**: the queue of obligations, oldest first, SLA badge, one click per option, free-text for questions.
  Answering writes the decision to the row/issue and resumes the loop; nothing here is a chat message.
- **Seats**: per rig, each seat's state, model (observed), permission mode, last activity, current row, pane tail
  (read-only, `tmux capture-pane`); actions: stop, launch, set permissions, send a message (with sender identity).
- **Task drawer**: brief, branch, worktree, MR with its pipeline jobs, verdict text, the event trail, links to issue
  and MR. This is where "who checked this" is answerable in one screen.
- **Settings**: projects/repos/rigs/labels/gates, concurrency caps, kill switch, alert routes.

Deliberately absent: a chart of tokens per day. Cost belongs in a report the user asks for, not a dashboard that
invites watching it.

## 11. API

`GET /api/board?project=…`, `GET /api/tasks/{iid}`, `GET /api/decisions`, `POST /api/decisions/{id}/answer`,
`POST /api/tasks/{iid}/dispatch`, `POST /api/rows/{qid}/resolve|handoff|close`, `POST /api/seats/{seat}/stop|launch`,
`POST /api/projects/{key}/pause|resume`, `GET /api/events?since=…`, `GET /api/stream` (SSE for board deltas).
Every POST requires a bearer token, is idempotent by an `Idempotency-Key`, and returns the `event_id` it wrote.
Read-only tokens exist for a wall display; the browser gets a session cookie, never a forge token.

## 12. Safety and idempotency

- **One writer**: act runs under a global lock; each project also holds a lease so two instances never dispatch.
- **Dry-run is the default on first boot** per project: intents are shown on the board for a day, then the owner
  flips `applied=1`. Same discipline as `dispatcher.py`'s `--apply`.
- **Kill switch** per project and globally; a paused project mirrors and keeps raising decisions but writes nothing.
- **No token in the repo**: forge access via `glab` (its own store) or a CI/service token in the environment; the
  app never prints a token into an event row (redact before insert, test it).
- **Every write is one event row**, with the command, exit code and a hash of stdout — the audit answers "who closed
  this epic" without reading a log file.
- **Never destructive**: the app does not delete branches or worktrees; it reports them for `worktree-audit.sh`.

## 13. Deployment

Two `systemd --user` units on the box that hosts the rigs (`the fleet box`): `fleet-console.service` (the binary, 127.0.0.1:8787,
`Restart=on-failure`, `EnvironmentFile` for tokens, mode 600) and nothing else — the timer of the old dispatcher is
replaced by the app's own decide loop. The board is served on loopback and reached over SSH (`ssh -L 8787:127.0.0.1:8787`)
or through the existing proxy; no public listener, no new TLS cert to renew.

Migration from the current loop, in order: run the console read-only against `app` for a week next to
`dispatcher.timer` (assert the same plan every tick — a golden test comparing both decisions), then
disable the timer, then let the console apply. The old state file is imported once (`dispatched`, `attempts`,
`nudges`, `closers`) so nothing is re-dispatched on cutover.

## 14. Phases

| Phase | Ships | Proof |
|---|---|---|
| P0 | registry + mirror + board read-only | board shows app's real queue and tasks; a golden test replays the last 200 lines of `dispatcher.log` and agrees on every skip reason |
| P1 | decide loop dry-run + decisions queue | one week of dry-run intents; the parked/red-gate/marker-leak detectors fire on the two real incidents (the 2-hour archive park, the 9102 flake) reproduced from fixtures |
| P2 | act loop for dispatch + resolve + merge watchdog | a real task dispatched end-to-end with the timer disabled, merged by the watchdog on the four facts |
| P3 | multi-project: second repo onboarded | two projects at once, capacity isolated, no cross-project iid collision |
| P4 | alerts, SLA, quota handling, cost report on demand | a parked decision escalates to Telegram within its SLA |

## 15. Decisions I made that you can overrule

1. **Go + SQLite, one binary** — matches the repos; a Postgres would mean a service to babysit. Overrule if a second
   box must share state (then: Postgres, same schema).
2. **Mirror instead of live reads on every screen** — a board that hits the forge per page load melts a self-managed
   GitLab and loses the "why" history. Staleness is shown (badge with the last sync).
3. **Decisions as records, not notifications** — a Telegram message is a nudge; the obligation must survive a
   restart, which is why the archive task stalled twice.
4. **No LLM in the loop, ever** — including "smart" ordering. The ordering rules are the owner's, and a model in
   that path makes the fleet unreproducible.
5. **The console does not own the merge judgement** — only the button, on four deterministic facts.
