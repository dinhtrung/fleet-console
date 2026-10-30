# Design — progress on the phone

## 1. One snapshot, one renderer, no second source of truth

Everything the message says already exists in the store, the mirror read and the project row; the feature is a
**projection**, not a new table.

```go
// internal/progress
type Snapshot struct {
    TakenAt time.Time
    Project ProjectBlock   // the whole row, field for field
    Rig     RigBlock       // name(s) + the live roster
    Epics   []Epic         // open epics with at least one open child
    InFlight []Item        // dispatch rows with no ended_at
    Pending  []Item        // open, not dispatched, not on hold, not an epic
    OnHold   []Item        // onhold label, or the project's own pause
    Counts   Counts        // true size of each bucket, even when a list is truncated
}
type ProjectBlock struct {
    Key, Name, Forge, Repo, RepoURL, DefaultBranch string
    Rigs        []string
    Cap         int
    OnHold, Act bool
    MainPath, WorktreePath string
}
type RigBlock struct {
    Names    []string
    Seats    []string // from the roster this tick read; empty when the rig did not answer
    Busy     int
    RosterErr string  // "roster unavailable: <why>" — a message, never a failed send
}
func Build(read mirror.TickResult, rows []store.DispatchRow, project store.ProjectRow, eff *config.Effective) Snapshot
func Render(s Snapshot, now time.Time) string
```

`Render` is deterministic (`now` is passed, never read), which is what makes it a golden-file test.

## 2. The message contract

Order is the order the operator asks: what landed → which project → which rig → what is running → what waits →
what is parked → what is it part of.

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

RIG fleet-console
  seats: dev-build-1, dev-build-2, dev-review · busy 2/3

▶ in progress (1)
  #63 web: Projects surface and New project dialog
      dev-build-2 · feature/63-p2-projects-surface · 41m

⏳ pending (0)
⏸ on hold (0)

epic: #64 create a project from the board — 1 of 2 done
```

Rules that make it readable at a glance:

- **The project block is the row, not a summary.** Every field the service reads appears, including paths, so a
  message pasted into a chat is enough to say what box, what checkout and what rig it refers to. `onhold` and
  `act` are printed as `yes/no` because "off" is the fact that explains a quiet fleet.
- **The rig line carries its roster** when the tick read one; when the rig did not answer, the line says
  `seats: roster unavailable: <why>` and the message still sends — a Telegram report is not the place to hide a
  dead rig, and it is not the place to fail because of one.
- One line per work bucket even when empty (`pending (0)`), so the shape never changes.
- At most **5** entries per bucket, then `+N more` on its own line; the count in the header is always the true
  count.
- Hard cap **3900** characters (Telegram allows 4096 and the header must survive). Truncation drops bucket lines
  from the bottom, then in-progress detail, never the project or rig block.
- Plain text, no Markdown: a failed entity parse turns a useful message into an error reply.
- `disable_web_page_preview` on, one bare board URL at the end.

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
2. **On demand** — `fleet-console --notify-report` mirrors nothing, reads the store, renders and sends. Exit 0 on
   a delivered message, 1 when the transport fails, 3 when notifications are not configured (the installer's
   "needs human" code).
3. **Optional digest** — `notify_interval: 30m` sends at most that often **while the snapshot hash changed**.
   Default `0` = off. It exists for a long unattended run, not as a heartbeat.

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
| Every required field appears | table test: key, name, forge, repo, url, branch, cap, onhold, act, both paths, rig name, seats |
| Empty buckets still print | snapshot with nothing in flight and nothing pending |
| Caps: 5 lines, true counts | 9 in flight → 5 lines + `+4 more`, header `(9)` |
| Length cap | 200 items renders ≤ 3900 chars and still starts with the project and rig blocks |
| Roster absent is reported | rig read error → the line says so, `Send` still called |
| Dedupe | same render twice → one `Send` |
| Change sends again | one item moves pending → in flight → a second `Send` |
| Never fails the tick | transport error → tick returns nil, `progress.failed` recorded |
| Disabled without env | empty token → zero `Send` calls, one boot line |
| Repo URL rules | github derived, gitlab derived + warning, `repo_url` verbatim |
