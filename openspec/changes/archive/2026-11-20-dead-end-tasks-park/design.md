# Design — a task a builder can't finish stops re-dispatching

## D1. The head is recorded where the attempt ends

The ready walk cannot tell an unchanged dead end from new work without knowing the head the last attempt
stopped at. The store already keeps every dispatch and when it ended; it gains one column, `end_head`, written
by `EndDispatchWithHead` in the same update that sets `ended_at`.

Reconciliation ends an attempt for one of three reasons: its change request merged, its queue row is terminal,
or the row has been absent for a full tick. A merged change request is its own record of the attempt, so that
arm records no head. The other two arms record the item's branch head from the same tick's git read — an item
with no branch records nothing, and the plan then keeps today's behaviour for it.

Ending an attempt with a head is not a claim about the item: a change request can open later, and the branch
check in the ready walk still runs first. The role of `end_head` is only to answer "did origin move since this
attempt stopped?".

**Why not record the head on the next tick instead?** Because the comparison is "unchanged since the attempt
ended", and a push between the end and the next tick would be recorded as the attempt's head, wrongly parking
new work. The end is the only moment that fact is true.

**Old rows.** Attempts that ended before this column existed carry `''`. An empty head is "the end recorded
no head", never "unchanged", so those items are adopted exactly as before. A migration that guessed a head
would park work nobody can prove is dead.

## D2. A matching head is the whole test

In the ready walk's branch-with-no-change-request arm, the newest dispatch for the item is checked:

- it is ended,
- it recorded a head,
- origin still reports that head (full sha or an abbreviation of at least seven characters — the
  `sameSHA` rule the verdict paths already use),
- no operator release answered after the end.

When all four hold, the item skips with `dead_end` and the detail `<branch>@<head>`. The skip goes in the arm
before the epic gate, so an item's own fact keeps its own skip, the same precedence the open/merged change
request arms have.

The comparison is by head, not by age: the old `StalledAfter` reset still describes a stalled attempt, but a
head that did not move is a stronger fact than 30 minutes passing. The attempt limit is untouched — it still
counts and still abandons at `MaxAttempts`; it just no longer authorises a fresh copy of the same dead end.

## D3. One question per (item, head), through the existing raise

`decide.PlanTick` turns each `dead_end` skip into an operator intent of kind `decision.dead_end`, carrying the
head as `HeadSHA`. Act routes it to `RaiseOperatorQuestion`, whose ledger key is already `(kind, subject,
head=<sha>;)`: a repeated tick is a `NoOp`, a moved head is a new question. That is the dedupe the brief asks
for, and it costs no new mechanism.

The question text names the item, the branch, the head and the three answers, and quotes the last row's
resolution when the queue read carries one: the builder's own words are what tells a person whether the item
is a dead end at all. The queue read (`rig queue list -A -a`) already carries `resolution`; it was simply
not decoded.

The brief for this kind says where the attempt stopped and what the answers do. The verdict instructions the
same raise carries for a missing verdict would be false on a dead end, so the kind selects the tail.

## D4. Release is an answer on the ledger

The question offers "release it". The board's answer path (`POST` on the decision row) writes a
`decision.answer` event whose subject is the question row, and that event is durable after the row closes —
which is exactly the problem the live queue read has: a closed row is no longer on the operator's part of the
queue, so the plan cannot see it.

`store.DeadEndReleases` folds the two ledger facts: the newest `decision.dead_end` per item (which names
`head=` and `row <qid>`), and a `decision.answer` on that row whose text says `release` (the words
`release`/`released`, so `onhold` never matches). The plan receives them as `Snapshot.ReleasedHeads`, the
same shape `RelayedHeads` already has, and `deadEndFor` lets such an item through.

**A release is spent, not standing.** It applies only when the answer is later than the end of the newest
attempt. A dispatch started after the answer becomes the newest attempt; when it ends, its end is later than
the release, so the same head parks again instead of re-dispatching forever. One release buys one attempt.

A release answered only in the rig's own queue (no board, no app event) leaves no fold row. The item stays
parked, and a new commit is the other release. An absence is safe; a guessed release would re-open the loop
this change exists to stop.

## D5. The manual label

Owner-only tasks need to be marked at creation time, before a builder fails on them. The project names the
label through the reserved `labels.manual` key — `labels: {manual: owner-only}` — and the default is `manual`.
The value is the forge label name, because the `labels` block is already a map that the config file, the
store row and the board's editor all round-trip; a new top-level key would have to be added to every one of
those, and a board edit today re-encodes the entry from its typed struct, so an unknown key would be silently
dropped.

The ready walk skips a labelled item with its own reason `manual`, beside `onhold`. A separate reason matters
because `onhold` means "paused", and an operator reading the board must be able to tell "a person does this"
from "nobody does this now". The skip carries the detail `owner-only task`, so the board's why-column reads
`manual — owner-only task`; the board also gives such an item its own state, `manual (owner)`, derived from
the recorded plan reason, and its own count — "blocked" would read as a fault and "waiting" as work the fleet
is working through.

The serial-epic rule must not let a `manual` child hold its epic's slot: the child is not work this fleet will
ever do in that slot, so holding on it would serialize the epic forever. Manual children are therefore
excluded both as candidates (they never claim the slot) and as holders (an open change request a person
started on their own manual child does not block the next sibling).

This is the second child's work (#527); the spec delta names it, and this change set is the epic's record.

## Risks and limits

- **A git read failure** leaves the branch list empty for that tick, so no item matches a branch and no dead
  end is seen; the item is adopted as today. The failure is already reported as the branch read's error; this
  change does not turn it into a second stale gate.
- **Multiple branches for one item.** The index keeps one head per item. A dead end is recognised only when
  the kept branch's head matches the recorded one, which is the branch the attempt used in every case seen;
  a second branch with a different head is new work and is adopted.
- **The first answer wins.** An operator who answers `hold` and later changes their mind must push or add the
  `onhold` label; the question is not re-raised for the same head.
