# Proposal — a verdict on a superseded commit is the reviewer's question, not the operator's

## Why

On 2026-11-13 every reject-and-fix round sent the operator a question nobody needed. The reviewer rejected a head, the builder pushed a fix, and the fix's re-review was queued at `dev-review`. Then the gate found the newest verdict note naming the old commit and raised "`#N: the merge gate needs a verdict (note …)`" to the operator seat. It happened six times in one afternoon: #376 three times, then #384, #393 and #395. Each time the right answer was the same: wait for the reviewer's verdict on the new head.

The tick treats a verdict that names another commit like a note it cannot read. `questionFromError` (`internal/act/verdict_question.go:159`) accepts `UnattributedRejectError` and `UnattributedAcceptError` (`internal/act/verdict.go:249-258`), and `tick.go:240` turns each into an operator question. The planner already handles the same fact correctly: its D2 arm (`internal/decide/plan.go`, `decision.stale`) asks the **reviewer** to review the new head, once per head. So the operator question is a duplicate sent to the wrong audience.

## What this changes

- A verdict whose sha is an **ancestor of the change request's head** is classified as *superseded*. The tick raises no operator question for it, stores no verdict for the head, and keeps the merge refused. The planner's reviewer row stays the only ask.
- A sha **outside** the head's history, a note with no sha, an unparseable note, or a failed ancestry check **still asks the operator**, unchanged. When unsure, the gate asks.
- Operator verdict questions already standing for a superseded head are closed `superseded` once a verdict on the current head is captured.

## Scope

`internal/act/verdict.go` and `verdict_question.go` (classification), `internal/act/tick.go` (the raise), `internal/act/verdict_question_reconcile.go` (closing standing questions), and tests. No new config key, no schema change, no change to the planner's `decision.stale` arm.

## Non-goals

- Not merging on any verdict that names another commit: that safety is unchanged.
- Not silencing the planner's reviewer row while a seat row is live. That is a deliberate rule (stale-verdict-visibility, D4) and this change keeps it.
- Not changing how the reviewer writes verdicts.

Epic: #412. Children: #413 (classification and raise), #414 (closing standing questions).
