# Design — progress on the phone

## 1. One snapshot, one renderer, no second source of truth

Everything the message says already exists in the store, the mirror read, the rig roster and the project row; the
feature is a **projection**, not a new table.

```go
// internal/progress
type Snapshot struct {
	TakenAt time.Time
	Project ProjectBlock  // the whole row, field for field
	Rig     RigBlock      // name(s), every seat with its state, and why it is idle
	Epics   []Epic        // open epics with at least one open child
	InFlight []Item       // dispatch rows with no ended_at
	Pending  []Item       // open, not dispatched, not on hold, not an epic
	OnHold   []Item       // onhold label, or the project's own pause
	Counts   Counts       // true size of each bucket, even when a list is truncated
}
type ProjectBlock struct {
	Key, Name, Forge, Repo, RepoURL, DefaultBranch string
	Rigs        []string
	Cap         int
	OnHold, Act bool
	MainPath, WorktreePath string
}
type RigBlock struct {
	Names     []string
	Seats     []SeatLine // every seat the roster returned, in roster order
	Busy      int
	Total     int
	Idle      bool   // no item in flight
	IdleWhy   string // one line, first matching rule in §2a; "" when not idle
	RosterErr string // "roster unavailable: <why>" — a message, never a failed send
}
type SeatLine struct {
	Session, LogicalID string
	Lifecycle          string // as the rig reports it: run, down, …
	LastActivity       string // as the rig reports it; "" when never seen
	Item               int    // the issue it holds, 0 when idle
	Branch             string
	Age                string // how long on that item; "" when idle
	IdleWhy            string // per-seat reason, "" when working
}
func Build(read mirror.TickResult, rows []store.DispatchRow, project store.ProjectRow, eff *config.Effective) Snapshot
func Render(s Snapshot, now time.Time) string
```

`read.Seats` already carries `rig.Seat{Session, LogicalID, Lifecycle, LastActivity}`; the renderer classifies, it
never invents. `Render` is deterministic (`now` is passed, never read), which is what makes it a golden-file test.

## 2. The message contract

Order is the order the operator asks: what landed → which project → which rig, and is it actually working → what
is running → what waits → what is parked → what is it part of.

```
✅ Done — fleet-console #62 backend: create a project from the board
   PR #65 merged · feature/62-p2-create-a-project · seat dev-build-1

PROJECT
  key: fleet-console        name: fleet-console
  forge: github             repo: dinhtrung/fleet-console
  url: https://github.com/dinhtrung/fleet-console
  branch: main              cap: 2
  onhold: no                act: on
  paths: main /home/dinhtrung/develop/fleet-console
         worktrees /home/dinhtrung/develop

RIG fleet-console · busy 2/3
  ▶ dev-build-1  #62 backend: create a project…      27m
  ▶ dev-build-2  #63 web: Projects surface…          41m
  ⏸ dev-review   idle — nothing to review

▶ in progress (2)
  #62 backend: create a project from the board
      dev-build-1 · feature/62-p2-create-a-project · 27m
  #63 web: Projects surface and New project dialog
      dev-build-2 · feature/63-p2-projects-surface · 41m
⏳ pending (0)
⏸ on hold (0)
epic: #64 create a project from the board — 1 of 2 done
```

And a fully idle fleet, which is the case the operator actually needs explained:

```
RIG fleet-console · idle
  ⏸ dev-build-1  idle — nothing pending
  ⏸ dev-build-2  idle — nothing pending
  ⏸ dev-review   idle — nothing to review
  why: idle — act is off: the board mirrors, nothing is dispatched
```

Rules that make it readable at a glance:

- **The project block is the row, not a summary.** Every field the service reads appears, including paths, so a
  message pasted into a chat is enough to say what box, what checkout and what rig it refers to. `onhold` and
  `act` print as `yes/no` because "off" is the fact that explains a quiet fleet.
- **Every seat appears, working or not.** An idle seat is the answer to "why is nothing moving"; omitting it
  turns the message into a description of the work that happens to be visible.
- **The rig header states `busy N/M`, or the word `idle`**, and when it is idle one `why:` line follows the seats
  (§2a). A rig that cannot answer prints `seats: roster unavailable: <why>` and still sends.
- One line per work bucket even when empty (`pending (0)`), so the shape never changes.
- At most **5** entries per bucket, then `+N more`; the count in the header is always the true count.
- Hard cap **3900** characters. Truncation drops bucket entries first, then in-progress detail, then seat lines —
  never the project block and never the rig header or its `why:` line.
- Plain text, no Markdown: a failed entity parse turns a useful message into an error reply.
- `disable_web_page_preview` on, one bare board URL at the end.

## 2a. Why the rig is idle

A reason is a sentence the operator can act on, so each one names the cause and, where it exists, the thing to
touch. The rig-level `why:` line uses the **first** matching rule:

| # | Condition (all from data the tick already has) | Line |
|---|---|---|
| 1 | `act: false` | `idle — act is off: the board mirrors, nothing is dispatched` |
| 2 | `onhold: true` | `idle — project on hold` |
| 3 | an active pause event from a quota decision | `idle — paused by a quota decision: <decision id>` |
| 4 | open decisions with no verdict | `idle — waiting on you: N open decisions (<#items>)` |
| 5 | no open item that is not an epic | `idle — no open work: every issue is closed` |
| 6 | roster answered, no seat in lifecycle `run` | `idle — no seat running: <n> seats reported <lifecycles>` |
| 7 | roster did not answer | `idle — rig roster unavailable: <why>` |
| 8 | otherwise | `idle — nothing dispatchable: <first plan skip reason>` |

Rule 8 is the catch-all and must quote the plan's own skip text (never `no free builder` — that is a busy fleet,
not an idle one), because a reason invented here would be the second source of truth this design exists to avoid.

Per-seat reasons, chosen in this order:

| Seat | Condition | Line |
|---|---|---|
| any | lifecycle is not `run` | `idle — seat <lifecycle>` |
| reviewer | no open draft change request awaiting a verdict | `idle — nothing to review` |
| builder | no pending item in the plan | `idle — nothing pending` |
| builder | pending items exist but this seat got none | `idle — waiting for a dispatch` |

"Nothing pending" and "waiting for a dispatch" are different facts: the first means the fleet has no work, the
second means it has work but this seat was not given any, which is the only case worth investigating.

## 3. Where the repo URL comes from

`forge_project` is a slug, not a URL, and a self-hosted GitLab cannot be guessed. So:

| Case | Value |
|---|---|
| `repo_url` set in config | used verbatim (the only correct answer for `gitlab.horisen.org`) |
| GitHub, no `repo_url` | `https://github.com/<forge_project>` |
| GitLab, no `repo_url` | `https://gitlab.com/<forge_project>` **and** a config warning at load: "set repo_url" |

## 4. Triggers

1. **Merge** — inside the applied tick, for each `MergeResult` (which already means "verdict accepted, checks
   green, PR merged"): build the snapshot **after** the merge and send one message. This is the "code xong"
   moment; no other event qualifies.
2. **On demand** — `fleet-console --notify-report` mirrors nothing, reads the store and the roster, renders and
   sends. Exit 0 on a delivered message, 1 when the transport fails, 3 when notifications are not configured.
3. **Optional digest** — `notify_interval: 30m` sends at most that often **while the snapshot hash changed**.
   Default `0` = off. It exists for a long unattended run, not as a heartbeat — which is why an idle rig does not
   start talking either: the hash only changes when the reason does.

## 5. Dedupe and failure

- A `progress.sent` event stores `<hash>:<messageID>`; the hash covers the rendered body minus the timestamp.
  Same hash → nothing sent, different → one new message. Messages are never edited: unlike a decision, a progress
  report is a photograph, and editing yesterday's photograph lies.
- The transport is bounded: 15s client timeout, **one** retry after 2s, then `alert: progress send: …` on stderr
  and a `progress.failed` audit event. **A failed send never fails the tick and never blocks a merge.**
- Missing `TELEGRAM_BOT_TOKEN` or `TELEGRAM_CHAT_ID`: one boot line —
  `notify: TELEGRAM_BOT_TOKEN/TELEGRAM_CHAT_ID not set; completion notices are off` — and every trigger is a
  no-op. Absent credentials are a configuration, not an error.

## 6. Wiring, and the gap it closes

`internal/alert` is complete and **never called**: no caller of `Escalator` exists in the binary, so decision
escalations do not reach the phone either. This change wires the shared client once:

```go
sender := alert.Telegram{Token: os.Getenv("TELEGRAM_BOT_TOKEN"), ChatID: os.Getenv("TELEGRAM_CHAT_ID")}
if sender.Token != "" && sender.ChatID != "" { /* Notifier + Escalator */ } else { log once }
```

Putting both on it is deliberate: a Telegram client that exists for progress but is unused for the decision it
was written for is a lie in the docs. Wiring the escalator is task 3.3.

## 7. Tests

| Promise | Test |
|---|---|
| The block is stable | golden file: fixture store + project row → `Render` with a fixed `now` |
| Every required field appears | table test: key, name, forge, repo, url, branch, cap, onhold, act, both paths, rig name, every seat |
| Empty buckets still print | snapshot with nothing in flight and nothing pending |
| Idle rig states why | table test over rules 1–8: one fixture per rule, each asserting the exact `why:` line |
| Idle seat states why | reviewer with no open draft → `nothing to review`; builder with no pending item → `nothing pending`; builder with pending items it did not get → `waiting for a dispatch`; dead seat → `seat down` |
| Caps: 5 lines, true counts | 9 in flight → 5 lines + `+4 more`, header `(9)` |
| Length cap | 200 items renders ≤ 3900 chars and still contains the project block, the rig header and its `why:` |
| Roster absent is reported | rig read error → the line says so, `Send` still called |
| Dedupe | same render twice → one `Send` |
| Change sends again | one item moves pending → in flight → a second `Send` |
| Never fails the tick | transport error → tick returns nil, `progress.failed` recorded |
| Disabled without env | empty token → zero `Send` calls, one boot line |
| Repo URL rules | github derived, gitlab derived + warning, `repo_url` verbatim |
