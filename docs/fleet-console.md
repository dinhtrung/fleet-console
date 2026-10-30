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

Both unblock the same way: post a verdict whose sha is the current head. Nothing is written to the
store for a refusal, so the next tick retries and the note stays until it is answered.

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
