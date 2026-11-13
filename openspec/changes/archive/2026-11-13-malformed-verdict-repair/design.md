# Design — malformed verdict repair

## D1. The fact is `verdict.malformed`, deduped per note

`internal/act/tick.go`, in the refusal branch: a parse failure writes kind `verdict.malformed`, subject `#<item>`,
detail `note=<id>; head=<sha>; line=<first non-empty line>`, key `note=<id>;` — through one helper beside
`noteUnattributedVerdict` (`tick.go:463-480`), so the trailing-`;` dedupe key and the best-effort write rule are
the same ones the ACCEPT and REJECT arms already use. The skip line prints only when the row is new.

*Rejected: reuse `verdict.unattributed`.* `verdict.unattributed` means "names a sha that is not this head"; this
one means "names no sha at all". One kind for both teaches an operator nothing about which repair to make, and the
detail grammar would need a `reason=` field that invites the next reader to parse prose.

*Rejected: keep printing the line every tick.* At a 60 s tick one malformed note produces ~1440 identical lines a
day. The audit row is the durable signal; repetition is not liveness.

## D2. The reviewer is asked first, with the literal line

`decide` emits `IntentRaiseDecision{Kind: decision.malformed_verdict, Audience: AudienceReviewer}` once per
`(note, head)`; `act` raises it as a queue row to the reviewer seat whose brief carries the repair verbatim:

```
The merge gate reads one line: ACCEPT <head> or REJECT <head>. Your note 6847905312's first line was
"## QA Verdict: ACCEPT ✅", which names no commit. Post a new comment whose first line is:

ACCEPT 4f1c2ab
```

The same text goes on the change request as one comment, so the artifact carries the instruction and the seat's row
points at it.

*Rejected: a comment on the change request only.* A seat does not read the pull request thread unless a row points
it there; the reviewer's row ended when it posted, so a comment changes nothing about what the seat is doing.

*Rejected: nudge the existing review row.* There is no row: a reviewer that answered ended its row, which is why
the note could be malformed in silence. Nudging needs something to nudge.

*Rejected: raise to the operator first.* The reviewer can fix it in one line and has the sha in hand; the operator
has to reconstruct both. The operator is the fallback, not the first call.

## D3. Re-nudge is the existing path, not a second one

Once the row exists, `internal/decide/plan.go:255-270` already nudges a live row whose destination is a seat every
`RenudgeAfter` (15 m) with a `RenudgeCooldown` (30 m). Nothing new is added; a malformed-verdict row is nudged like
any other seat row.

*Rejected: a dedicated re-nudge interval for malformed verdicts.* Two cadences for one kind of waiting is how a
project ends up with a reviewer nudged twice in one tick and an operator wondering which line to trust.

## D4. One question per head: malformed supersedes `no_verdict`

In `plan.go:296-330` the `verdict == nil` arm raises `decision.no_verdict`. It gains a first check: if the snapshot
carries a malformed note for this item and this head, the malformed question is the one raised and `no_verdict` does
not fire. `askedForHead` (`plan.go:526-548`) keeps its meaning for both — a live row naming this head is the same
question standing.

*Rejected: raise both.* Two questions about one fact, one of them false ("has no verdict" when the reviewer plainly
tried), and the operator answers the wrong one.

## D5. The deadline is `StaleEscalateAfter`, then the operator

New constant beside `RenudgeAfter`/`RenudgeCooldown` (`plan.go:27-30`):

```go
// StaleEscalateAfter is when a question a seat has been nudged about twice
// becomes a human's: two full nudge cycles.
StaleEscalateAfter = RenudgeAfter + RenudgeCooldown // 45m
```

When the malformed row's age (`row.CreatedAt`, which the nudge does not move) passes it, `decide` raises
`decision.stall` to `AudienceOperator` (`human@kernel`), once per `(item, head)`, with: the note id, the line as
written, the head under review, the literal repair, and the two lawful moves — **cast the verdict on the item
drawer** (`POST /api/items/<n>/verdict` writes `ACCEPT <sha>` at the head) or **instruct the reviewer**.

*Rejected: give the human a merge button.* A hand merge skips the four facts and destroys the verdict→merge
evidence the next investigation needs; the human's lawful move is a verdict, after which the gate merges on its own.

*Rejected: escalate on a fixed wall clock from the note's timestamp.* The nudge clock is the one the fleet already
understands, and `RenudgeAfter + RenudgeCooldown` is the same 45 minutes the stale-verdict design asks for, so one
constant serves both.

## D6. The parser does not get lenient

`verdictNote` stays anchored and sha-required; `ACCEPT ✅`, `ACCEPT` alone, and a four-character sha stay refusals.

*Rejected: infer the head when the note names none.* The inference would merge whatever landed last on the strength
of a note that never named it — the exact fall-through #89 fixed, one layer up.

## D7. The snapshot carries the fact, because `decide` may not read the forge

`plan` is pure rules over a snapshot. The malformed facts are event rows, so:

- `internal/store/queries.go`: `MalformedVerdicts(ctx, projectID)` returns, per item, the newest
  `verdict.malformed` event (`NoteID`, `Head`, `Line`, `FirstSeen`) — a read over the existing event table, no schema
  change;
- `mirror.BuildSnapshot` gains that slice and fills `Snapshot.MalformedVerdicts map[int]…`;
- `cmd/fleet-console/main.go` passes it where it already passes dispatch rows and verdicts.

*Rejected: let `decide` take a store handle.* The plan has no database access by design, and giving it one to read
a single kind would be the second query path in a package that must stay pure.

*Rejected: derive the fact from the skip lines.* `report.Skipped` is prose for the journal, not a fact the plan can
trust, and it disappears when the dedupe takes the line away.
