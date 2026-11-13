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

The skip is visible and it now asks. One `change_request.no_item` event and one skip line per (change request,
head) name the dead end; when the title or branch named an item, the same tick raises **one decision row**
(`decision.no_item`) instead of leaving the change request to wait forever. The row carries the change request,
the head and the named item with its state, and its answer depends on that state: an **open** item is a body edit
away — add `Closes #<n>`, or open a follow-up item if the work is not that item — while a **closed** item cannot
be closed again, so the answers are merge by hand or a follow-up item with the branch re-homed to
`feature/<new item>-`. A mention never becomes an association: the row asks a human and sets no item. A resolved
row does not silence a later head.

Anything unknown refuses and names the missing fact. The reviewer keeps the judgement; the app keeps the button.

**Adoption: a hand-dispatched item still merges.** The four facts used to key on a `dispatch` row, so an item whose
queue row was created by hand — `rig queue create`, outside the app — never merged: the watchdog skipped its change
request with `closes no dispatch`. When the store holds no dispatch row, the same four facts are now enforced
against the change request itself, plus two guards that exist only because there is no row to trust: the head branch
must start with `feature/<item>-`, and a live dispatch for the same item on a *different* branch refuses rather than
picking a winner. Adoption records `change_request.adopted` before the merge and the ordinary `merge` event after,
and it invents no dispatch row. Readiness stays the integrator's job: the watchdog marks a draft ready and merges,
but a human still decides what is worth merging.

### The repair ask does not outlive its premise

When an accepted change request cannot merge because its head conflicts, the app raises one repair row to the
human: rework the branch, or defer the item. The row is reconciled with what raised it:

- **One open row per item.** A rebased head is a note on the row the ledger's newest `decision.merge_repair`
  event names — saying which head the ask now covers — never a second row. The `(item, head)` ledger still
  prevents a re-raise at a head that has already been asked.
- **The row dies with its premise.** On every applied tick, an open row is resolved — through the same path a
  human answer uses — when the item is closed, the change request is closed or merged, or the current head is
  green with an ACCEPT naming it. The resolution names the fact that ended it.
- **A failed reconcile is a skip line.** A rig that refuses the note or the resolve does not fail the tick; the
  next tick retries.
- **A resolved row does not silence the next conflict.** A later head at the same change request raises exactly
  one new row.

### Rows that ask a human

A queue row whose destination is a human seat (`human@kernel`, `human-<suffix>@host`) is judged by the person, so the
daemon requires `summary` and `evidence_ref` on it: the evidence is the durable artifact the row's answer is judged
against. The app passes the brief path it just wrote for that row — the operator question, permission refusal, silent
dispatch, ambiguous dispatch and merge-repair raises all carry theirs. `scripts/human-route-probe.sh` is the command
that proves the daemon agrees: on a throwaway daemon it refuses the create without `--evidence-ref`
(`human_route_fields_required`), accepts it with the brief path, and reads the ref back off the row.

### Reading a verdict

A verdict is a note that **states** one: a line whose first word, once the wrapping is stripped, is
`ACCEPT` or `REJECT`, followed by the sha it is about — `ACCEPT <sha>`, `REJECT <sha>`, each free to
carry a reason after the sha (`ACCEPT <sha> (PR #78, #66)`).

The gate reads the note's first **1024 characters**, line by line, and strips what models put in
front of a verdict before it looks: fenced code and HTML comments, a link (`[ACCEPT](https://…)` →
`ACCEPT`), emphasis, list and quote decoration, icons, and one short label from a closed list —
`QA`, `Verdict`, `Review`, `Result`, `Decision`. So `QA ACCEPT <sha>`, `🎯 **ACCEPT <sha>**`,
`[ACCEPT](…) <sha>`, `## QA Verdict: ACCEPT <sha>`, and a `Verdict:` line with `ACCEPT` and the
sha on the next lines are all the same verdict. The label list is closed on purpose: a rule that
accepts any word in front of the token reads `Ready to accept` as a verdict.

**Prose stays prose.** The token has to *open* a line. `Not ready to ACCEPT <sha> until CI is
green` is a sentence about a verdict, not a verdict, and the gate ignores it — no verdict, no
question, no merge.

**What names the head.** A sha of seven or more hex characters names the head when it is a prefix
of it: `ACCEPT 7f76634` names head `7f766346bebb…` and is attributed to it. A sha of four to six
characters names the head only when it is a prefix of it, and then the stored verdict carries the
full head. A shorter sha that is not a prefix of the head names nothing the gate can act on, and the
note is skipped — a typo is not a question for a human.

**What the gate cannot read, it asks.** Three shapes have no fact to act on: a verdict word with no
sha, both polarities in one note (`ACCEPT` and `REJECT` on separate lines), and a line that negates
itself (`Verdict: not ACCEPT <sha>`). Each raises **one** row, keyed on the note and the head, to
the project's operator destination — the seat named by `verdict_relay.seat` when the project sets
`verdict_relay.mode: operator`, and `human@kernel` otherwise. The row names the note, the head, the
line the scan read, and the three answers: post `ACCEPT <head>`, post `REJECT <head>`, or send the
request back for review. The app never writes that verdict and never merges on the question. If the
row cannot be created for the seat, it is created once for `human@kernel` instead, so a renamed seat
cannot swallow the question.

A tick repeating every minute asks once: the ledger dedupes on `note=<id>; head=<head>;`, and a new
note or a moved head is a new question. Unanswered questions are the rig's stuck sweep to escalate —
the app keeps no second timer.

To send these questions to a seat instead of the human queue, give the project the block — the seat
must exist in the rig, and the spelling is the one the rig uses for a destination (dots become
dashes: node `operator.agent` on rig `kernel` is `operator-agent@kernel`):

```yaml
projects:
  - key: fleet-console
    # … forge, repo, rig, paths …
    verdict_relay:
      mode: operator
      seat: operator-agent@kernel
```

`mode: scribe` routes only the relay rows to the seat and keeps questions on `human@kernel`; no
block keeps today's behaviour. A seat that cannot take the row is answered by one `human@kernel`
row instead, so the question is never swallowed.

**A verdict about another commit also asks.** An `ACCEPT` or `REJECT` whose sha is not the head ends
the scan without a verdict (it never falls through to an older note about the head) and raises the
same question, with `accept at the head`, `reject`, and `re-review` as the answers. The old skip
line and the `verdict.unattributed` audit event survive: the typed error is what tells the tick
which question to ask.

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

**The board shows what is waiting.** A question is only useful if the person it is addressed to
can see it, so each project's board row lists its unanswered `decision.verdict_question` entries:
the item, the change request, the note, the head, how long it has waited and the seat it went to.
The list is read from the ledger — it survives a restart — and joined with the project's last
queue read: a row the queue says is finished drops out, and a row the queue has not seen stays,
because the ledger's word is the only fact then. A project with nothing waiting says so in one
line, so a stuck project cannot pass for an idle one. It is read when the board renders, like the
rest of the board; there is no second poller.

**A question about a head the branch has left behind closes itself.** When the reviewer posts a
verdict on the head the change request carries now, the reconcile closes every standing question
about an older head as `superseded`, and the resolution names the verdict and the head it landed
on. Nobody is going to answer a question about a commit the branch no longer carries — on
2026-11-13 the operator closed six of them by hand. A question about the current head keeps
waiting: an older head's verdict says nothing about it, and neither does an older question come
back to life when the head moves.

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

The pair lives on the project row as well as in the file. The board's create and edit carry
`relay_mode` and `relay_seat` and write both layers, so a project that exists only as a row can
still set its relay. The resolver answers from the row first and the file block second, which is
what lets the two layers meet: an *absent* block leaves the row alone — a boot apply from a
hand-edited file never erases what the board set — while an *empty* block (`verdict_relay: {}`)
is an explicit off and clears it.

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

### What `tick: panicked:` means

A journal line beginning `tick: panicked:` is a **contained** failure: one tick of the mirror
loop crashed, the loop caught it, and the process is alive. The value after the prefix is the
panic itself, and the stack that follows names the site — read it top-down until the first
frame inside this repository, which is the code the tick was running. The loop returns to the
base cadence after a panic, and when the wait changes the cadence line says so:
`cadence … reset: tick: panicked: …`.

Nothing else happened. A panic is not an item, not a decision, and not a page: the tick raised
nothing, so nothing shows up on the board. The journal line and the fact that the box is
supervised are the whole response.

That is the requirement underneath the line: **the console must run under a supervisor that
restarts it** — the `fleet-console.service` user unit, with `Restart=on-failure`. A contained
panic still means the tick did nothing; an unsupervised box that starts the console from a task
runner with no restart policy is dark after *any* panic, and that is the failure this section
exists because of: the loop contained the crash, and nothing was left to restart it.

## What it refuses to be

- **No model in the dispatch path.** Ordering is a contract, not a judgement call.
- **No second source of truth for the rig.** The rig stays authoritative; the mirror is stamped on every screen.
- **No destructive automation.** It never deletes a branch or a worktree; it reports them for an audit script.

## The rig lifecycle

When the tick reads the rig and the daemon answers "not running", that is not a stale roster: a daemon that is
not running means every rig on it is down, so a project with ready work starts its rig on demand and the launch
carries the reason — `daemon down; N ready item(s)` — into the journal and the `rig.started` event. A cold
`rig up` gets five minutes (`rig.StartTimeout`) because it boots a daemon, the kernel and every seat, while
every other rig call keeps the 20 s read budget; a start is attempted at most once per five-minute backoff
(`act.StartBackoff`), and the backoff is memory-only — a restart forgets it, which can only delay a start by one
window. The stop half stays dark while the daemon is down: nothing that does not exist can be judged quiet, and
no quiet streak opens for a rig the tick could not see.

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
