# Design — a refusal is recorded once, explained, and never stalls silently

## D1. Two facts, two error identities, one family

`CaptureVerdict` refuses two different things with one type today (`internal/act/verdict.go:77-87`): a REJECT whose
sha is another commit, and a first line whose sha does not parse. Only the first is a *fact about this head*; the
second is a malformed note.

Add `UnattributedRejectError{NoteID, Written, Head string}`, returned by the sha-mismatch branch (`verdict.go:182-185`).
`UnattributableVerdictError` keeps the parse-failure case. Both keep `Is(ErrUnattributableVerdict)`, so the exported
family and every existing test keep their meaning:

```go
func (e *UnattributedRejectError) Is(target error) bool { return target == ErrUnattributableVerdict }
```

`Rejected:` adding a `Kind` field to `UnattributableVerdictError` — one type carrying a discriminator makes the
caller switch on a string, and `errors.As` already answers the same question at compile time.

`Rejected:` calling the parse failure a "rejection" too, and recording both — a truncated sha is a reviewer typo
that the next note fixes; a record per typo is noise, and the parse case already names the line verbatim.

## D2. The record: the existing kind, the existing grammar, once per note

The rejection writes the **same** `verdict.unattributed` kind with the **same** detail
(`note=<id>; sha=<written>; head=<head>`) and the **same** dedupe key (`note=<id>;`) the ACCEPT arm already uses
(`tick.go:400-409`). One helper, two callers; no new kind, so a reader of the audit surface does not learn a second
vocabulary for one fact.

The print rule follows the record: the full `#<item> verdict: <err>` line is appended to `report.Skipped` only when
the helper wrote the row. A later tick over the same note prints nothing; a different note or a new head writes and
prints again, because the key is the note.

`Rejected:` a process-local `map[noteID]bool` — the tick loop lives in-process, but a restart would re-print, and
the event table already answers "have I seen this note" without new state.

`Rejected:` printing a short "still unattributed" line every tick — it keeps the noise and adds a second format to
grep for. Silence after the first line is the point; the audit row is the durable signal.

## D3. The sentence: the order, both shas, the unblock

The reason becomes:

```
the note is older than the head: this REJECT names <sha>, the head is <head>; a verdict naming <head> unblocks the merge
```

It keeps the note id and the quoted line (the error's `NoteID` and `Line` fields), so the existing test that asserts
"names the note and both shas" still passes with updated text.

**No commit time.** `forge.ChangeRequest` carries no timestamp (`internal/forge/forge.go:132-150`); printing "the head
advanced at 21:30" would mean adding a field to both adapters for a log line. `Rejected:` adding `HeadCommittedAt` in
this change — it is a forge-shape change with its own tests, and the sentence is honest without it.

`Rejected:` keeping the current wording and adding a hint after it — the first clause is what an operator reads, and
it says the wrong thing ("a rejection names X" reads as the reviewer's mistake).

## D4. The stall: reuse the operator path, do not invent one

The tick already routes `AudienceOperator` to `Actions.RaiseOperatorQuestion`
(`internal/act/tick.go:252-276`), which posts a queue row to `human@kernel` and dedupes on `kind + "#<item>" +
"head=<sha>;"` (`internal/act/review.go:115-128`). The stall is that, with one more predicate in `decide`:

- the row is live and names the current head (it is the `decision.stale` row);
- `now - row.CreatedAt > StaleEscalateAfter`;
- `row.LastNudgeAt` is non-zero (at least one nudge has gone unanswered).

`StaleEscalateAfter = 45 * time.Minute`, beside `RenudgeAfter` and `RenudgeCooldown` (`plan.go:27-30`): a 15-minute
first nudge and a 30-minute cooldown mean the second nudge is the honest "still nothing" moment. It is a constant,
not a per-project knob — one box, one team size, and a knob here would be tuned by whoever is paged.

Kind `decision.stall`; text names project, item, change request, head, the age and the nudge count, and the two
answer paths (`POST /api/items/<n>/verdict`, `fleet: accept` / `fleet: reject`). `RaiseOperatorQuestion` appends the
head and the answer paths to the brief, so the question itself stays one sentence.

`Rejected:` the blocked-merge notifier (`report.BlockedMerges` / `notify.sendBlocked`) — a page is write-only; the
operator's answer already has two working paths on the item, and a queue row is where the board can see it.
`Rejected:` re-raising `decision.stale` to the reviewer more often — the failure is that the seat stopped answering,
so asking it again is the definition of the bug.

## D5. The title and the branch are evidence, never keys

MR !683 named `#448` in its title and branch while its body said `Refs #448`. The gate must not merge on either:

- `Ready` rewrites the GitLab title to strip `Draft:` (`internal/forge/gitlab.go:573-587`), so the title is mutable
  by the app itself;
- the branch is builder-controlled, and `merge-adopted-crs` already uses `feature/<n>-` only as a **guard**, never as
  an association (`openspec/changes/merge-adopted-crs/design.md` D4: "a title-only reference is how an item gets
  silently forgotten").

So `forge.ChangeRequest` gains `Title` (GitLab `title`, GitHub `title`) **for the diagnostic only**: when the body
closes nothing, the skip line and the event may say which item the title or branch mentioned. Nothing in `Merge`,
`MergeAdopted`, `CaptureVerdict` or `decide` may read the field.

`Rejected:` a branch fallback that associates the change request with an item — it would merge a `Refs` change
request and then close the item, which is exactly what the builder avoided by writing `Refs`.
`Rejected:` requiring the title to carry `Closes #n` as a second accepted shape — two grammars, and the mutable one.

The skip is deduped per `(change request, head)` by `HasEvent(kind, subject, "cr=<n>; head=<sha>; named=<n|none>;")`.
Subject is `#<item>` when an item was named, else `cr-<number>`, so the board groups it under the item a human would
search for.

## D6. The brief: give partial work a lawful move

`briefTemplate`'s `## Publish` (`internal/act/actions.go:98-117`) gains two sentences:

1. the gate finds the item only through `Closes #<item>` in the body; a body that only references the item is
   invisible to it;
2. if the item cannot be finished in this attempt, do not open a change request that only references it — hand off
   with the candidate sha, the commands and what remains, so the coordinator splits the item into sub-issues.

The finished case is unchanged: one draft change request, `Closes #<item>`, no merge, hand off with the sha, the
commands and what could not be checked.

`Rejected:` teaching the app to split issues — decomposition is the coordinator's act, and an app that creates
issues from checklist boxes would be a second planner.
`Rejected:` allowing `Refs` change requests to merge without closing — the item would stay open while its code
landed, which is the `my-stack.md:195` failure in the other direction.
