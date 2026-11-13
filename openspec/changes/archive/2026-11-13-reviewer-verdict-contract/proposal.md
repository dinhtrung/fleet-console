# Proposal — the reviewer's verdict is a contract the app enforces, not a sentence a model remembers

## Why

The verdict contract lives in two places, and only one of them travels across models:

- prose in each rig's `guidance/fleet-review-charter.md` — "your only write to the forge is **one comment per
  review** whose first line is the verdict";
- a strict regex in `internal/act/verdict.go:26`,
  `^(ACCEPT|REJECT)\s+([0-9a-f]{7,40})\b`, anchored to the first non-empty line.

Under Claude/sonnet the prose is followed. Under any other model — omp on deepseek, an OpenRouter seat, pi — the
reviewer slips in ways the contract does not recognise. Measured on 11/11/2026 with the shipped regex:

```
"ACCEPT 3f2a1b9c"             token=true  note=["ACCEPT 3f2a1b9c" "ACCEPT" "3f2a1b9c"]
"**ACCEPT 3f2a1b9c**"         token=false note=[]          <- skipped silently
"## Verdict: ACCEPT 3f2a1b9c" token=false note=[]          <- skipped silently
"`ACCEPT 3f2a1b9c`"           token=false note=[]          <- skipped silently
"> ACCEPT 3f2a1b9c"           token=false note=[]          <- skipped silently
"- ACCEPT 3f2a1b9c"           token=false note=[]          <- skipped silently
"ACCEPT: 3f2a1b9c"            token=true  note=[]          <- refused, tick retries forever
"ACCEPT — 3f2a1b9c"           token=true  note=[]          <- refused, tick retries forever
"ACCEPT"                      token=true  note=[]          <- refused, no sha at all
```

Two different failures, one symptom — the item waits:

- **Decoration** makes the note not verdict-shaped at all. `CaptureVerdict` (`verdict.go:85`) `continue`s, nothing
  is captured, nothing is refused, and no line anywhere says a review was posted and ignored.
- **A missing or mistyped sha** on a verdict-shaped line returns `ErrUnattributableVerdict` (`verdict.go:34`),
  which is correct — but the only trace is an `act-skip` line, one per tick, for hours.

The fallback that should catch both asks the wrong seat. `internal/decide/plan.go:244` raises "open change request
with no verdict and no live row. Accept it, reject it, or reopen the seat?" — a question for a human — but
`internal/act/review.go:69` creates that row with `Destination: reviewer`, and the event ledger dedupes on the text
(`review.go:31-38`). So the same seat that just failed to answer is asked once, in its own language, and never again;
the operator only sees a board that says the item is waiting.

And the brief a reviewer receives (`review.go:60`) names neither the change request number nor the head sha, let
alone the command. Composing `ACCEPT <sha>` is a reasoning task every model can fail, while running a command with
the sha already in it is not.

## What changes

- The parser **normalises leading decoration** before matching the token; the sha still has to name the head, and an
  unattributable verdict is still refused rather than skipped.
- **A missing verdict becomes an operator question**, once per `(item, head sha)`, naming the two ways a human can
  answer: the board's Approve/Reject (`POST /api/items/:n/verdict`) and the `fleet: accept|reject` reply on the item.
- **A re-review row is raised only from a verdict that parsed** and names an older sha, once per `(item, head sha)`.
- **The review brief carries the facts to execute**: the change request number, the head sha verbatim, and the
  literal command that posts the note with the sha substituted.
- No new evidence path. The note on the change request stays the record; a queue row stays a claim.

## Losses, named

- Decoration tolerance means a note that used to do nothing can now merge. Bounded by sha equality: it can never
  widen *which head* a verdict is about.
- One more operator question while a reviewer seat is misbehaving — attention spent deliberately, on the thing that
  is broken.
- The brief grows by a handful of lines, and every reviewer seat of every model reads them.

## Capabilities

### New Capabilities

- `review-verdict` — what counts as a verdict, how decoration is normalised, what happens when a reviewer posts
  none or posts one that cannot be attributed, and what the brief hands the seat.

### Modified Capabilities

None. `decision-routing` is not a shipped capability; this delta brings its own requirement for the question's
destination.

## Impact

- `internal/act/verdict.go` — the parser's normalisation.
- `internal/decide/plan.go` — the missing-verdict intent and its audience.
- `internal/act/review.go`, `internal/act/tick.go` — the operator question, the richer brief, and the dispatch that
  writes each.
- Tests in `internal/act` and `internal/decide`; no config key; no forge-interface change.
