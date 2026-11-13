# Proposal — a refusal is recorded once, explained, and never stalls silently

## Why

Three facts about one gate, all measured on this box on 2026-11-12 (tsim, project
`horisen/mnp/engine/traffic-simulator-v2`):

**1. An unattributable verdict is refused correctly and reported badly.** At 21:27:50 the reviewer posted
`REJECT a7463125…`; at 21:30:21 the builder pushed the fix `5690552` — a direct child of `a7463125`. For the next six
minutes every 60 s tick printed the same line:

```
act-skip: tsim: #455 verdict: act: a verdict note cannot be attributed to this head: note 37482:
a rejection names a7463125…, which is not the head under review (5690552…) ("REJECT a7463125…")
```

The system was healthy: the reviewer answered `ACCEPT 5690552…` at 21:37:59 and MR !682 merged at 21:39:12. But the
line blames the note ("a rejection names X, which is not the head") when the fact is the reverse — the head moved
after the note — and nothing says what unblocks it. It is printed once per tick because `report.Skipped` is
re-emitted verbatim (`cmd/fleet-console/main.go:886`) while the note set is unchanged. The ACCEPT twin is deduped on
the audit surface (`verdict.unattributed`, one row per note, `internal/act/tick.go:400-409`); the REJECT arm writes
no row and dedupes nothing. 86 such lines in seven days across two projects (tsim `#395` 10, `#426` 8, `#447` 7,
thuocla `#58` 6): this is the normal shape of a builder pushing while a review is in flight, not an anomaly.

**2. The stall has no last mile.** `decision.stale` is raised once per `(item, head)` to the reviewer
(`internal/decide/plan.go:325-343`) and re-nudged to the same seat every 30 minutes (`plan.go:249-265`,
`RenudgeAfter` 15 m, `RenudgeCooldown` 30 m). If the reviewer never answers, the same seat is nudged forever and no
human is ever asked.

**3. A whole change request can be invisible.** MR !683 (tsim) carried title `Draft: tsim #448: …`, branch
`feature/448-…`, body `Refs #448`. The reviewer posted `ACCEPT 7e868054…` (note 37508) on a green pipeline (57243,
9/9). The tick resolved no item — `ParseCloses` matches `Closes/Fixes/Resolves` only, `ClosesItem` was 0 — and
`continue`d (`internal/act/tick.go:129-135`): no verdict captured, no `Ready`, no merge, **no line**. Three hours
later the change request was still a draft and dispatch 150 still `active`. The brief had told the builder to open
the change request with `"Closes #448"` (`internal/act/actions.go:98-117`); the builder delivered part 1 of 5 and
chose `Refs`, the honest body for partial work, which is the one body the gate cannot read. The brief gives partial
work no lawful move.

The gate is strict about the two things it must be strict about (an unattributable note is never merged; a change
request that closes nothing is never merged). What is missing is not permissiveness — it is **one record per fact,
one honest sentence, and a human when the seat stops answering**.

## What changes

- **One error identity per fact.** A REJECT that names another sha becomes `*act.UnattributedRejectError`; a first
  line whose sha does not parse stays what it is today. `errors.Is(err, act.ErrUnattributableVerdict)` remains true
  for both, so nothing downstream loses the family.
- **One record per (item, note).** The rejection writes `verdict.unattributed` with the existing grammar
  (`note=<id>; sha=<written>; head=<head>`, `;` terminator) and the line is printed on the tick that wrote it, not
  on every tick after it. A second note, or a new head, is a new record.
- **One honest sentence.** The refusal says the note is older than the head, names both shas, and says that a verdict
  naming the head unblocks the merge. No commit time: `forge.ChangeRequest` carries none.
- **One human question per stalled head.** A live stale-review row older than `StaleEscalateAfter` (45 minutes, two
  nudge cycles) with at least one nudge raises `decision.stall` to `human@kernel` through the operator path the tick
  already has; deduped per `(item, head)`.
- **One visible skip for a change request that closes nothing.** `change_request.no_item` once per
  `(change request, head)`, with the item named in the title or branch when there is one — as a **diagnostic**,
  never as an item key.
- **One sentence in the brief.** The gate reads `Closes #<item>` in the body; a partial attempt hands off with the
  candidate sha and what remains, instead of opening a `Refs` change request.

## Losses, named

- **The journal gets quieter.** An operator who greps `cannot be attributed` will see one line per note instead of
  one per tick. The count is no longer a liveness signal; the `verdict.unattributed` rows are.
- **One more unsolicited question.** `decision.stall` lands in `human@kernel`, the same inbox as
  `decision.no_verdict`; an item can carry two questions at once, told apart by kind and head. The quiet-notify rules
  do not apply to it.
- **One more field on the forge type.** `forge.ChangeRequest` gains `Title` for the diagnostic. It is a shape
  change to the two adapters; the risk is that a later change reads it as an item key, which this spec forbids.
- **Nothing becomes mergeable that was not.** An unattributable note, a `Refs` body and a draft change request stay
  unmerged. This change set buys legibility and an escalation, not throughput.
