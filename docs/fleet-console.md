# fleet-console — architecture in one page

The full contract is `openspec/changes/fleet-console-v1/`. This is the map.

## Three loops, one writer

```
              ┌──────────────────────────── fleet-console (one Go binary) ───────────────────────────┐
  forge  ───▶ │ mirror loop  ──▶ SQLite ──▶ decide loop ──▶ act loop ──▶ forge / rig / git adapters   │
  rig    ───▶ │ (read, ~15 s)     (durable)   (pure, 1 min)  (audited)                               │
              │                                    │                                                  │
              │                          REST + SSE API ──▶ web board (static, embedded)               │
              └───────────────────────────────────────────────────────────────────────────────────────┘
```

- **Mirror** reads the forge (items, links, merge requests, pipelines), the rig (queue rows, seats) and git
  (branches on origin). It writes only mirror tables, and every screen shows when it last ran.
- **Decide** is pure: same mirror in, same intents out. That is what makes a dispatch bug reproducible offline.
- **Act** is the only writer, serialized per project, and every action becomes one audit event.

## Why decisions are records

The expensive failure is not a red pipeline. It is a review verdict that closed without handing work back: the
branch still has an open merge request, no row is alive, every later task reads *blocked*, and the fleet looks idle
while one question waits for a human. A notification about that is a nudge; a **decision row** with an owner, an SLA
and one-click options is the thing that survives a restart. The board is mostly a view of that table.

## The merge watchdog

A merge happens only when four facts are true, re-read at the moment of the merge:

1. a verdict `ACCEPT <sha>` exists for this dispatch;
2. that sha is still the merge request's head;
3. the head pipeline is green — read from the job list, respecting `allow_failure` — or the diff is docs-only;
4. the merge request is mergeable and its body says `Closes #<id>`.

Anything unknown refuses and names the missing fact. The reviewer keeps the judgement; the app keeps the button.

### Reading a verdict

A verdict is a note whose **first line** starts with the token and a sha: `ACCEPT <sha>`,
`REJECT <sha>`, and either may carry a reason after the sha — `ACCEPT <sha> (PR #78, #66)`.
Prose that merely mentions a token is not a verdict, and a note is never scanned beyond its first
line.

Two outcomes are worth knowing because they stall a merge rather than being silently absorbed:

- **A verdict that cannot be tied to the head blocks.** `REJECT` with a truncated, absent or
  mistyped sha refuses the merge and names the note in the tick log, rather than falling through
  to an earlier `ACCEPT`. Before this, a reviewer's withdrawal was dropped whenever the sha did not
  parse and an older accept merged the change.
- **A rejection naming another sha blocks too.** An `ACCEPT` for a superseded head is skipped — it
  says nothing about the current commit — but a `REJECT` for one is treated as a statement that
  outlives its sha. That is stricter than an accept and it is deliberate: in a multi-attempt thread
  the reviewer's objection may still stand.

Both unblock the same way: post a verdict naming the **full** head sha — the whole 40-hex commit id,
not an abbreviation. An abbreviated sha is refused like any other unattributable verdict, and one
that is long enough to parse but is not the head is skipped. Nothing is written to the store for a
refusal, so the next tick retries and the note stays until it is answered.

### Closing the epic

After a merge, the tick asks one more question: was that item the last open sub-issue of its parent
epic? If it was, the app comments on the epic naming what landed, closes it, and records one
`epic.closed` event with the child count. Two merged items that share a parent produce **one** close,
not two — the tick tracks the epics it has already closed.

The rule is deliberately conservative in three places:

- **A short read never closes an epic.** Children are counted whether open or closed, so "the
  snapshot held none" cannot be mistaken for "all closed". A forge read that returned the epic but
  not its sub-issues leaves the epic open and journals `no sub-issues in the snapshot`.
- **A parent that is not an epic records nothing.** Most merged items have no parent at all, and a
  journal line per merge would bury the rollups that matter. Three epic-shaped skips *are* recorded,
  because each is a real answer: still open, no sub-issues in the snapshot, already closed.
- **A failed comment or close is a skip line, never a failed merge.** The merge is the fact; the
  rollup is a courtesy. The epic stays open and the next tick retries.

The epic being closed is a consequence of the merge, not a condition of it: a rollup that cannot
run never un-merges anything.

## What it refuses to be

- **No model in the dispatch path.** Ordering is a contract, not a judgement call.
- **No second source of truth for the rig.** The rig stays authoritative; the mirror is stamped on every screen.
- **No destructive automation.** It never deletes a branch or a worktree; it reports them for an audit script.

## The board

```
┌ fleet-console ────────────────────────────────────────────────────────────────────────────┐
│ ● app   ▶ 3 building · 1 review · 2 waiting on you      ● docs  ⏸ paused (quota)        │
├──────────┬────────────────────────────────────────────────────────────────────────────────┤
│ PROJECTS │ TASK                              STATE     SEAT     AGE   WHY BLOCKED         │
│ app  12 │ #51 connection-pool store        building  build-1  24m   —                   │
│ docs   4 │ #53 connection-pool form         queued    —        3m    waits #52          │
│ infra  0 │ #42 archive timestamp-seed       waiting   —        2h    ⚠ parked: MR green, │
│          │                                                            no verdict          │
├──────────┴────────────────────────────────────────────────────────────────────────────────┤
│ ▲ 2 decisions need you    [ open the oldest ▸ ]      oldest 2h 04m · SLA 30m ⚠ breaching │
└───────────────────────────────────────────────────────────────────────────────────────────┘
```

Status, then the reason, then the action. Nothing on this screen is a chat message.
