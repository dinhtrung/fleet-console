# Tasks — malformed verdict repair

A task is done only with the command that proved it, quoted in the pull request. Epic: GitHub
`dinhtrung/fleet-console#<epic>`; the code tasks are its children T1–T3, T4 documents them.

## 0. Ground rules — fixed vocabulary

**Audit kind.** `verdict.malformed` — a note whose first line starts with `ACCEPT`/`REJECT` after decoration is
stripped, and whose sha does not parse. Subject `#<item>`; detail `note=<id>; head=<sha>; line=<first non-empty
line>`; dedupe key `note=<id>;` (trailing `;` is the terminator). Written through one helper beside
`noteUnattributedVerdict` (`internal/act/tick.go:463-480`), best-effort: a ledger failure never fails the tick.

**Decision kinds.** `decision.malformed_verdict` — the reviewer's question, `AudienceReviewer`.
`decision.stall` — the operator's, `AudienceOperator` (destination `human@kernel`). `decision.no_verdict` keeps
its kind and stops firing while a malformed note stands for the same head.

**Constant.** `StaleEscalateAfter = RenudgeAfter + RenudgeCooldown` (45 m), in `internal/decide/plan.go` beside
`RenudgeAfter` (15 m) and `RenudgeCooldown` (30 m). Not configurable per project.

**Snapshot field.** `Snapshot.MalformedVerdicts map[int]MalformedVerdict` with `{NoteID, Head, Line, FirstSeen}`,
filled by `mirror.BuildSnapshot` from `store.MalformedVerdicts(ctx, projectID)`. The plan reads nothing else.

**The literal the reviewer must post.** In the brief and in the change-request comment, verbatim:

```
The merge gate reads one line: ACCEPT <head> or REJECT <head>. Your note <id>'s first line was
"<line>", which names no commit. Post a new comment whose first line is:

ACCEPT <head>
```

**Never.** No parser leniency (a note that names no sha stays refused), no merge on the operator's word, no second
re-nudge cadence, no store handle inside `internal/decide`, no schema change. If a task appears to need one, stop
and report on the epic instead of inventing it.

## 1. T1 — the malformed note is a recorded fact

- [ ] `internal/act/tick.go`: the parse-failure branch (`~150-180`, today `errors.As` on the two unattributed
      types only) also handles `*UnattributableVerdictError` and writes `verdict.malformed` once per note through a
      new helper `noteMalformedVerdict`, mirroring `noteUnattributedVerdict`. The skip line prints only when the row
      is new — the same `alreadyNoted` shape the other two arms use.
- [ ] `internal/act/verdict.go`: no parser change. `UnattributableVerdictError` already carries `NoteID`, `Line`,
      `Reason`; the head is not on it, so the caller passes `head` (`in.CRs`' `HeadSHA`) into the helper.
- [ ] `internal/store/queries.go`: `MalformedVerdicts(ctx, projectID)` — one read over the event table, newest
      `verdict.malformed` per item, returning note id, head, line and the event's `created_at`.
- [ ] Tests: `internal/act/verdict_malformed_test.go` — the observed note (`## QA Verdict: ACCEPT ✅`) writes one
      row; a second tick over the same note prints no line and writes no second row; a note naming a wrong sha
      still writes `verdict.unattributed`, not this kind.

Evidence: `go test ./internal/act/ ./internal/store/ -run 'Malformed' -count=1 -v` green, and a two-tick test that
asserts `report.Skipped` holds the line on the first tick and nothing on the second.

## 2. T2 — the reviewer is asked, once per (note, head)

*Blocked by T1.*

- [ ] `internal/mirror/mirror.go` + `cmd/fleet-console/main.go`: `BuildSnapshot` takes the malformed slice and
      fills `MalformedVerdicts`; the two call sites in `main.go` pass it.
- [ ] `internal/decide/plan.go`: in the `verdict == nil` arm (`~311-330`) check `MalformedVerdicts[item]` for this
      head first and emit `IntentRaiseDecision{Kind: "decision.malformed_verdict", Audience: AudienceReviewer,
      ChangeRequest: cr.Number, HeadSHA: cr.HeadSHA}` with the literal from §0. `decision.no_verdict` does not fire
      for that (item, head). `askedForHead` keeps both deduped.
- [ ] `internal/act/review.go`: the raise builds the brief with the literal line and the note's own text; the
      existing `Note`/`Comment` path posts the same text on the change request once per note.
- [ ] Tests: `internal/decide/plan_test.go` — a malformed note for the head yields one
      `decision.malformed_verdict` and no `decision.no_verdict`; a live row naming the head yields neither;
      `internal/act` — the raise writes one queue row to the reviewer and one comment.

Evidence: `go test ./internal/decide/ ./internal/act/ -run 'Malformed|NoVerdict' -count=1 -v` green; the test
name that asserts "no `decision.no_verdict` when a malformed note stands" quoted in the pull request.

## 3. T3 — after two nudge cycles, the operator

*Blocked by T2.*

- [ ] `internal/decide/plan.go`: `StaleEscalateAfter` beside `RenudgeAfter`/`RenudgeCooldown`; when a live
      malformed-verdict row for this (item, head) is older than it (`row.CreatedAt`, untouched by nudges), emit one
      `decision.stall` to `AudienceOperator` carrying the note id, the line as written, the head, the literal
      repair, and both lawful moves (item drawer verdict, or instruct the reviewer).
- [ ] Dedupe per `(item, head)` on the ledger (`decision.raise` detail already carries both) so a permanent
      malformed note asks once, not every tick after the deadline.
- [ ] The row is not re-targeted: the reviewer's row stays open and the operator's question is a second row to a
      different destination — the reviewer can still fix it while the human reads.
- [ ] Tests: `internal/decide/plan_test.go` — a malformed row 44 minutes old raises nothing to the operator; 46
      minutes raises exactly one `decision.stall`; a parseable verdict posted in between stops both the nudge and
      the escalation.

Evidence: the three table rows above, printed by name from `go test ./internal/decide/ -run 'Stale|Malformed'
-count=1 -v`.

## 4. T4 — what each party sees

*Blocked by T2 and T3.*

- [ ] `docs/fleet-console.md`: one subsection beside the verdict/review material — the reviewer's one line
      (`ACCEPT <sha>` / `REJECT <sha>`, first non-empty line, undecorated), what a malformed note produces
      (`verdict.malformed`, one line, one reviewer question, one change-request comment), and the operator's
      deadline: at 45 minutes the question moves to `human@kernel`, and the human's move is a verdict on the item
      drawer — not a hand merge.
- [ ] `docs/forge-token.md` is not the home for this; no new file.

Evidence: `grep -n 'verdict.malformed\|malformed' docs/fleet-console.md` returning the subsection, and
`openspec validate --all --strict | tail -3` still green.

## 5. Verification before the pull request leaves draft

- [ ] `go test ./... -count=1` green.
- [ ] `CGO_ENABLED=0 go build -trimpath -tags embedboard -o /tmp/fc-check ./cmd/fleet-console` green.
- [ ] `openspec validate malformed-verdict-repair --type change --strict` and `openspec validate --all --strict |
      tail -3` green, totals line quoted in the pull request.
- [ ] The pull request body carries: `Closes #<task>`, the red/green pair for T1's dedupe test, and the two test
      names from T2/T3.
