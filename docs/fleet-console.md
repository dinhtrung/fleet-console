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

That line is the only thing the gate reads: a change request whose body merely references the item (`Refs`,
`Part of`, a title mention) is never merged, undrafted or recorded, so partial work hands off with what remains
instead of opening one.

Anything unknown refuses and names the missing fact. The reviewer keeps the judgement; the app keeps the button.

**Adoption: a hand-dispatched item still merges.** The four facts used to key on a `dispatch` row, so an item whose
queue row was created by hand — `rig queue create`, outside the app — never merged: the watchdog skipped its change
request with `closes no dispatch`. When the store holds no dispatch row, the same four facts are now enforced
against the change request itself, plus two guards that exist only because there is no row to trust: the head branch
must start with `feature/<item>-`, and a live dispatch for the same item on a *different* branch refuses rather than
picking a winner. Adoption records `change_request.adopted` before the merge and the ordinary `merge` event after,
and it invents no dispatch row. Readiness stays the integrator's job: the watchdog marks a draft ready and merges,
but a human still decides what is worth merging.

### Rows that ask a human

A queue row whose destination is a human seat (`human@kernel`, `human-<suffix>@host`) is judged by the person, so the
daemon requires `summary` and `evidence_ref` on it: the evidence is the durable artifact the row's answer is judged
against. The app passes the brief path it just wrote for that row — the operator question, permission refusal, silent
dispatch, ambiguous dispatch and merge-repair raises all carry theirs. `scripts/human-route-probe.sh` is the command
that proves the daemon agrees: on a throwaway daemon it refuses the create without `--evidence-ref`
(`human_route_fields_required`), accepts it with the brief path, and reads the ref back off the row.

### Reading a verdict

A verdict is a note whose **first line** starts with the token and a sha: `ACCEPT <sha>`,
`REJECT <sha>`, and either may carry a reason after the sha — `ACCEPT <sha> (PR #78, #66)`.
Prose that merely mentions a token is not a verdict, and a note is never scanned beyond its first
line.

**What names the head.** A sha names the head under review when it is the full 40-hex commit id,
or an abbreviation of at least 7 hex characters that is a prefix of it: `ACCEPT 7f76634` names
head `7f766346bebb…` and is attributed to it. Fewer than 7 characters never parses as a verdict
at all, and whatever the note wrote, the stored verdict carries the full sha.

Two outcomes are worth knowing because they stall a merge rather than being silently absorbed:

- **An accept naming another head is skipped and reported.** An `ACCEPT` whose sha is not a
  prefix of the head says nothing about the current commit, so the scan ends there instead of
  falling through to an older note that happens to name the head. The tick reports the refusal
  twice over: one `act-skip: … verdict: ACCEPT <written> does not name head <head> (note <id>)`
  line per tick in the log, and one `verdict.unattributed` audit event per note on the ledger.
- **A rejection naming another sha blocks.** A `REJECT` for a sha that is not the head is
  treated as a statement that outlives its sha: it refuses the merge and names the note. That is
  stricter than an accept and it is deliberate: in a multi-attempt thread the reviewer's
  objection may still stand.

Both unblock the same way: post `ACCEPT <the head>`. The board's Approve button and the reviewer
brief already emit the full head sha, so the abbreviated form is the exception, not the
documented path. Nothing is written to the store for a refusal, so the next tick retries and the
note stays until it is answered.

### When the app asks a human

The operator question — *this change request has no verdict; approve or reject it* — is not
raised on every quiet tick of an unreviewed head. The plan asks only when no row is working
the item, and two facts keep it quiet:

- **Work in flight.** A live builder or reviewer row for the item suppresses the question,
  whatever sha the row's text carries: the seat holding the row is the work itself, so
  asking a human to judge a head mid-review would only page someone for the same thing
  twice. A row addressed to a human seat never counts as work — it is a question standing,
  not a seat at work, so the operator's own pending question cannot silence the next one.
- **A question already standing for that head.** A live question row naming the item and
  the head is the same question still unanswered; the plan stays quiet while it stands. A
  question that names a different head is not standing in the way — a moved head asks again,
  once.

**What "names the head" means in row text.** The same rule the note parser applies to
verdicts applies to row bodies: an abbreviation of at least 7 hex characters that is a
prefix of the head counts as naming it. Both sides call the one matcher, `forge.SameSHA`,
so a question body saying `head cca2508c` stands for the full 40-hex sha the record
carries, and no other spelling of "names" exists in the code.

**A stale verdict breaks the silence — to the reviewer.** When the head has moved on from
the newest verdict, the plan raises `decision.stale` and asks the *reviewer* to re-review
the new head even while another row is live, because a re-review is a question, not work.

### When a review is not a verdict

A reviewer can finish a review without producing anything the grammar above can read: prose that
hedges, no notes at all, or a note about a different head. When that happens — the change request
and its item are open, the head carries no verdict, and a terminal reviewer row names that head —
the plan carries one relay intent, and the act loop turns the finished review into work someone can
answer. It reads the review's notes through the forge, writes a brief carrying them verbatim with
the exact note to post, and creates one queue row. One relay per item and head, deduped on the
ledger: the same head never asks twice, and a moved head is a new fact that asks again.

The project opts in with a `verdict_relay` block per project (off when absent — today's behaviour):

```yaml
verdict_relay:
  mode: scribe
  seat: verdict-scribe
```

The two modes differ in what each one may do:

- **`scribe`** sends only the relay row, to a seat of the project's own rig — that rig already
  holds the project's queue, culture and budget, so the project can size the seat to its own cost.
- **`operator`** additionally routes the project's *operator questions* (the
  `decision.no_verdict` raises above) to the configured seat instead of `human@kernel`. The kernel
  operator is shared by every rig and is the human's front door, so pointing project work at it is
  a decision each project makes for itself, never a default. The relay row's destination is the
  configured seat in both modes; only the questions switch.

The note the relay posts carries a **`via:` marker**: `ACCEPT <sha>` on the first line,
`via: <seat>` on the second. When the gate captures a verdict whose second non-empty line is
exactly `via: <seat>`, the `verdict.capture` audit event's detail gains `; via=<seat>` — the
ledger records that this verdict reached the forge through the relay seat rather than the
reviewer's own hand. A missing or malformed marker records nothing and never blocks.

If the review does not decide, the relay never invents a verdict: it answers its own row
(`denied` / `no-follow-on`) naming what it read, writes nothing on the forge, and the operator
question the next tick raises is the escalation. **The relay never merges — the merge gate does.**
It also never closes, never edits code, and never re-reviews; the note it posts is judged by the
gate's own facts on a later tick, like any other verdict.

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

### The registry, edited from the board

The Projects surface edits, pauses and deletes a project over the API:
`PUT /api/projects/{key}`, `PUT /api/projects/{key}/onhold`, and
`DELETE /api/projects/{key}?confirm=<key>`. The pause writes the database's
`onhold`, never the file — it survives a restart and an `--apply-config`, and
only `--apply-config --sync-onhold` copies the file's `onhold:` over it, so a
file that pins `onhold: true` wins when that flag is passed. A board delete is
the same cascade as `--prune` for the one project named: the file entry goes
first, then the project's rows, `event` rows untouched, and it is refused with
the dispatch id while a dispatch is live. The edit carries the file's own field
names; the key is immutable, so a rename is a delete and a create.
