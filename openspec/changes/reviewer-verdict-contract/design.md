# Design — the reviewer verdict contract

## D1. Normalise decoration in the parser, not in the charter

`CaptureVerdict` is the one place every model's output passes through, whatever runtime wrote it and whatever prose
the rig's charter carried at launch. The charter is a startup file per rig (`guidance/fleet-review-charter.md`),
read once at seat launch, and each rig keeps its own copy — so a wording fix has to be repeated per rig and lands
only for the next launch. The parser fix lands once, in the binary, for every seat.

`stripDecoration` removes, in any order, from the start of the line: `#`, `>`, `-`, `*`, `+`, backticks, `**`, `__`,
`_`, and whitespace; then one optional separator (`:`, `—`, or a dash) and the whitespace after it. Then the
existing token and sha matching run unchanged.

Rejected: a charter rewrite saying "no markdown" — prose guidance is exactly what the non-Claude models failed to
follow, and it would have to be edited per rig.

Rejected: stripping decoration anywhere in the first paragraph — a verdict is a line, and widening the window turns
a sentence into a decision.

## D2. Keep the sha, keep the refusal

Decorating `**ACCEPT <sha>**` is safe because the sha still has to equal the head under review. Dropping the sha
would not be: the merge gate merges at a sha, so a verdict with none cannot be attributed — the fallback is merging
"whatever is there", which is how a stale approval lands on a moved head.

The refusal of a verdict-shaped line with an unparseable sha stays, and so does its reason: skipping it would fall
through to an older `ACCEPT` and merge over a newer typo'd `REJECT` (`verdict.go:66-80`).

Rejected: accepting `LGTM` / `ACCEPT` with no sha — it makes the gate the thing that guesses the head.

Rejected: treating an unparseable verdict as "not a verdict" — the two failures need different messages to the
operator, and `ErrUnattributableVerdict` already names the note.

## D3. A missing verdict is an operator question, not a reviewer row

Today `plan.go:244` raises the right text and `review.go:69` sends it to `Destination: reviewer`. Three things go
wrong at once: the human never receives the question, the seat that just failed is asked to answer it, and
`HasEvent(kind, subject, text)` (`review.go:31-38`) makes the whole thing a one-shot — if the seat fails again, no
second question is ever raised.

The fix is an audience on the intent, not a second intent kind: `Intent.Audience` with `AudienceReviewer` (the zero
value, so every existing caller keeps its behaviour) and `AudienceOperator`. `RaiseOperatorQuestion` writes the row
with `Destination: "human@kernel"`, the shape `raiseAdoptionDecision` already uses (`internal/act/adopt.go:170-186`).

Dedupe: event kind `decision.no_verdict`, subject `#<item>`, and a detail that carries `head=<sha>`. A moved head is
a new fact — the reviewer was asked about a different commit — so it asks again; an unchanged head never repeats.

The question's body names the item, the change request number, the head sha, and the two answers that already work:
the board's drawer Approve/Reject (`POST /api/items/:n/verdict`, `internal/web/verdict.go:102`, which posts
`ACCEPT <sha>` for the head) and the `fleet: accept|reject` reply on the item. Both write a note, so the record is
unchanged.

Rejected: relaying the seat's queue-row resolution into a note — that launders a claim into evidence. The row is
what a seat *says*; the note is the record the gate reads, and a human's own verdict already has a route.

## D4. A re-review row survives for a verdict that parsed on an older head

The stale-verdict case (D2 of the operating model) is a different audience and a different dedupe key: the reviewer
has already produced a parseable verdict, so asking it to look at the new head is a real job. It keeps
`IntentRaiseDecision` and `Destination: reviewer`, deduped by `(item, head sha)`.

Rejected: one intent kind with a flag for both — the two cases differ in audience, in dedupe key and in what the
operator must do, and a boolean would encode that badly.

## D5. The brief carries the command, not just the format

The reviewer's row is written from `plan.go` where both facts are already in scope: `cr.Number` and `cr.HeadSHA`
(`plan.go:249`). Passing them through the intent lets the brief print the exact line and the exact command:

```
gh pr comment <n> -R <repo> --body 'ACCEPT <sha>'
glab api -X POST 'projects/<id>/merge_requests/<iid>/notes' -F 'body=ACCEPT <sha>'
```

A literal command with the sha already in it is executable by a weaker model; a formatting rule is a reasoning task.
The brief keeps one sentence for the fallback: if the command cannot run, answer on the row with `ACCEPT <sha>` and
the app raises the operator question rather than guessing.

Rejected: another paragraph of charter prose — same D1 reasoning, and the sha changes per review, so a static
document cannot carry it.

## D6. The change-request note stays the only verdict record

Nothing in this change reads a queue row, a handoff summary or a seat's claim as a verdict. The note remains the
record, `CaptureVerdict` remains its reader, and the operator question exists so that a human — not the app — turns
a missing verdict into one.
