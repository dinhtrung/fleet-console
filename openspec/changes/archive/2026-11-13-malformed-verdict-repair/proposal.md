# Proposal — a malformed verdict is recorded, taught back to the reviewer, and escalated only on a deadline

## Why

**1. The failure is loud to the journal, silent to both seats.** A reviewer posted a note whose first line was
`## QA Verdict: ACCEPT ✅` (`note 6847905312`). `stripDecoration` removes the heading and the `QA Verdict:` label
(`internal/act/verdict.go:66-79`), leaving `ACCEPT ✅`; `verdictToken` matches, `verdictNote`
(`verdict.go:27-31`) finds no sha, and `CaptureVerdict` returns
`UnattributableVerdictError{Reason: "the first line starts with a verdict token but its sha does not parse"}`
(`verdict.go:206-212`).

The tick then does what the code comment says it does — `internal/act/tick.go:163-166`: *"The parse failure keeps
today's behaviour: it is a malformed note a reviewer typo causes, not a fact about this head, so it is neither
recorded nor deduped"*. Concretely:

- the `#<item> verdict: …` skip line prints on **every** tick over the same note (60 s tick → ~1440 lines a day),
  with no ledger entry to go quiet on, which buries every other skip in the journal;
- **the reviewer is never told.** It believes it accepted; the review row already ended, so nothing re-asks it;
- **the operator is told something untrue-for-this-case, or nothing.** `decide/plan.go:311-330` raises
  `decision.no_verdict` to `human@kernel` when a change request has no verdict — text
  *"change request #N has no verdict for head X. Approve or reject it on the item drawer…"* — which describes the
  reviewer as silent when it in fact answered, and carries neither the note nor the line to repair. And
  `askedForHead` (`plan.go:526-548`) suppresses even that when a live row already names the head, so the item can
  sit with a question nobody has.

**2. The cheapest fixer is the one not asked.** The note is one line wrong, the reviewer holds the head in its own
transcript, and the repair is `ACCEPT <sha>`. Everyone else — an operator reading the board — has to reconstruct
the fact from a journal line.

**3. The gate must not learn to guess.** `ACCEPT ✅` names no commit. Attributing it to the current head would merge
whatever landed last on the strength of a note that never named it, which is the same class of mistake #89 fixed
for rejections (`verdict.go:76-79`: skipping an unattributable note "falls through to an older ACCEPT and merges a
change the reviewer has since superseded").

**4. The escalation already half exists and is the wrong shape.** `decision.no_verdict` proves the operator path
works; what it lacks is the truth about *why* there is no verdict, and a deadline. Nothing today says "a note
claimed a verdict and did not parse" anywhere a human reads.

## What changes

- **The fact is recorded once.** Kind `verdict.malformed`, deduped per note on the event ledger like the other
  unattributable verdicts, printed on the tick that wrote it and silent afterwards.
- **The reviewer is taught, not nagged blind.** One queue row to the reviewer seat whose brief carries the exact
  line to post (`ACCEPT <head>`), and one comment on the change request with the same literal — once per
  `(note, head)`. The existing nudge path re-asks every 15/30 minutes; no second reminder mechanism.
- **One question per head.** While a malformed note stands for the same head, `decision.no_verdict` does not also
  fire: the malformed question is the true one.
- **A deadline, then the operator.** `StaleEscalateAfter = RenudgeAfter + RenudgeCooldown` (45 minutes) after the
  question was raised, `decision.stall` goes to `human@kernel` with the note id, the line as written, the head, the
  literal repair, and the two lawful moves — cast the verdict on the item drawer, or instruct the reviewer.
- **No parser leniency and no hand merge.** The refusal stays; the only thing added is the repair loop.

## Losses, named

- **One more queue row per malformed note.** The reviewer seat gets work it would not have had; that row is the
  point, and it closes when a parseable verdict appears (the existing reply resolves it).
- **One more audit kind** (`verdict.malformed`) beside `verdict.unattributed`, because the two repairs differ:
  one wrong sha, one no sha.
- **The operator is asked later, not sooner** — up to 45 minutes of reviewer time before a human is involved. That
  is two nudge cycles, the same patience the stale-verdict design asks for.
- **A malformed note still blocks the merge.** Nothing merges on a note that names no commit; the loss is latency,
  not correctness.
- **The journal loses its per-tick repetition.** An operator who greps `verdict:` will see one line per note
  instead of one per tick; the count stops being a liveness signal, and the `verdict.malformed` row becomes it.

## Capabilities

- **New: `verdict-repair`** — what happens between "the reviewer wrote something that claims to be a verdict" and
  "a human is asked".

## Impact

`internal/act/verdict.go` (no parse change), `internal/act/tick.go` (the refusal's bookkeeping),
`internal/store/queries.go` (one reader), `internal/mirror/mirror.go` + `cmd/fleet-console/main.go` (snapshot
carries the fact), `internal/decide/plan.go` (supersede, question, deadline), `internal/act/review.go` (the brief),
`docs/fleet-console.md` (what the reviewer and the operator each see). No schema change; one new constant.
