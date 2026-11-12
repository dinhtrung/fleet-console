# Design — verdict relay

## D1. The trigger is the reviewer row ending, not a clock

`forge.ChangeRequest` carries no timestamps (`internal/forge/forge.go:129-146`), so "wait 10 minutes" would need a
new durable wait state on every tick. The row already carries the fact we want: a queue row that names this head,
addressed to the reviewer seat, has **ended** — the reviewer considers its review done, whatever it wrote.

Trigger, evaluated in `decide` next to the existing `decision.*` arms (`internal/decide/plan.go:301-336`):

1. the change request is open and not merged;
2. the item is open and has a verdict-less head — no stored verdict whose sha is the head (`newestVerdict` +
   `sameSHA`, so an abbreviated record still counts);
3. a row exists that names this head (`askedForHead(s.Rows, item, head)`) and is in a terminal state, addressed to
   the reviewer seat;
4. no other relay for this (item, head) is already in the ledger.

`Rejected:` an `after:` duration — it invents a wait state and a timestamp the snapshot does not have, and it fires
either too early (the note is still being written) or too late (a reviewer that ended its row is done).

## D2. The relay writes the record; the gate merges

The relay's only deliverable is one note in the existing grammar. `act.Merge` still re-reads the four facts — verdict
sha equals the head, checks green, mergeable, branch `feature/<n>-`, body `Closes #<n>` — and still owns the draft
flip, the marker label, the item close and the ledger.

`Rejected:` letting the relay merge with `gh pr merge` — that skips all four facts, leaves the `dispatch` row open and
writes no `verdict` row, so the board starts lying about who is working on what. A model's judgement must never be
the thing that bypasses the checks a human wrote.

## D3. A relayed verdict is marked with a second line, `via: <seat>`

The note stays `ACCEPT <sha>` on line 1, so every existing reader works. Line 2 carries `via: verdict-scribe@rig` or
`via: operator-agent@kernel`; capture stores it (`verdict.capture` detail gains `; via=<seat>`) and an empty or absent
`via:` means the reviewer wrote it itself.

`Rejected:` a distinct grammar (`ACCEPT via=scribe <sha>`) — it breaks the board's own route, every existing note and
the parser's anchoring, for a field that fits on the next line. `Rejected:` inferring the author from the forge
comment — seats and the operator share one account, so the forge cannot tell them apart.

## D4. Operator mode is a destination switch, not a second intent

`Audience` already splits who a decision is for (`internal/decide/plan.go:89-103`), and `tick.go:256` maps
`AudienceOperator` to `RaiseOperatorQuestion`, which hard-codes `Destination: "human@kernel"` (`internal/act/review.go:151`).
Operator mode changes exactly that resolution: when the project's relay mode is `operator`, the destination is
`verdict_relay.seat`; otherwise it stays `human@kernel`.

`Rejected:` a new `decision.operator_no_verdict` intent — two triggers to keep in step, two dedupe keys, and the same
fact stored twice.

## D5. The scribe lives in the project's rig; the operator is opt-in per project

`scribe` names a seat in the project's own rig because that rig already holds the project's queue, its culture and
its model budget, and a project can size the seat to its own cost. `operator` is the kernel's seat, which is shared
by every rig on the box and is also the human's front door — routing project work into it is a decision each project
makes for itself, never a default.

`Rejected:` one mode that always uses the kernel operator — all merges of all projects would serialise through one
session that also talks to the human, and its queue would become a mix of project work and conversation.
`Rejected:` a hard-coded seat name: the rig names its own seats, and a box that renames one must be able to say so.

## D6. INCONCLUSIVE is a row answer, never a note

When the review does not decide — prose that hedges, no notes at all, a note about a different head — the relay
answers its queue row (`denied` / `no-follow-on` with one sentence naming what it read) and writes nothing on the
forge. `decide` then raises the operator question for that head, which is today's path.

`Rejected:` translating "unclear" into `REJECT` — a reject is the builder's work list, and inventing one sends work
in a direction nobody asked for. `Rejected:` leaving the row open: a relay that never answers is indistinguishable
from a broken one.

## D7. Dedupe per (item, head) through the event ledger

One relay per (item, head), recorded as `verdict.relayed` with detail `head=<sha>; row <qitemId>`, matched with
`HasEvent(kind, "#<item>", "head=<sha>;")` — the trailing `;` is the terminator, so `head=aaaa111;` can never match
`head=aaaa1110;`. A moved head is a new fact and asks again; the same head never asks twice.

`Rejected:` a `relayed` column on `dispatch` — a migration and a second place the truth can live, for something the
ledger already answers.

## D8. Off unless configured, and `seat` is required when a mode is set

An absent block is today's behaviour: no relay, `human@kernel` keeps every operator question, no new rows. A block
with a mode and no seat is a config error at load (`config.ValidateProject`), not a runtime surprise.

`Rejected:` a default seat — an upgraded binary must not start creating rows to a seat that may not exist.

## D9. The brief carries everything; the relay reads no state itself

The brief holds the review's notes **verbatim** (the full text, one block per note, with the note id), the full head
sha, the item and the change request number, the exact command to post the verdict, the `via:` line it must add, and
the sentence that the only other permitted answer is INCONCLUSIVE. The relay is not told to run `gh` to fetch the
review: a second read can disagree with the head the gate is judging, and it would need forge credentials in a seat
that has no business holding them.

`Rejected:` a brief that says "go read PR #N" — it re-derives the head, and on a moved head it writes a verdict for a
commit nobody judged.

## Tests that prove the promises

| Promise | Test |
|---|---|
| Off means off | project with no block → no relay intent, no row, same `decision.*` behaviour |
| Trigger needs an ended reviewer row | row pending → no relay; row done → one relay intent for that head |
| Verdict at head suppresses the relay | stored verdict at head (full or abbreviated) → no relay |
| One relay per head | two ticks at one head → one intent, one row; a moved head → a second |
| The brief is enough | brief contains every note body verbatim, the 40-char head, the `via:` line, the command |
| Relay never merges | intent carries no merge action; a relayed `ACCEPT` merges only through `act.Merge`'s four facts |
| INCONCLUSIVE escalates | relay answers the row → `decision.no_verdict` raised for the operator; nothing written on the forge |
| Operator mode | `mode: operator` + seat → `RaiseOperatorQuestion` destination is the seat; `mode: scribe` → `human@kernel` for questions |
| Marker is stored | a note with `via:` line 2 → capture event detail carries `via=`, absent line → no marker |
| Config discipline | `seat` missing with a mode → load error naming the field; `--print-config` prints mode and seat |
