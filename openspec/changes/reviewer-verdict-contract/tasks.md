# Tasks — the reviewer verdict contract

A box is ticked only with the command that proved it, quoted in the pull request.

## 0. Ground rules

- Build gate for every task: `go test ./...` and `go vet ./...`. No config key, no forge-interface change, no new
  CLI.
- Fixed vocabulary — do not invent a second name for any of these:
  - `stripDecoration(line string) string` in `internal/act/verdict.go`; the parse entry point stays
    `CaptureVerdict`.
  - `decide.Audience` with `AudienceReviewer` (zero value) and `AudienceOperator`.
  - Event kinds `decision.no_verdict` (operator question) and `decision.stale` (re-review, unchanged).
  - Operator row destination is the literal `human@kernel`; dedupe detail carries `head=<sha>`.
  - The brief's verdict line is exactly `ACCEPT <sha>` / `REJECT <sha>`; the sha is the head, lower-case.
- Stop and report instead of inventing: if a forge has no single command that posts a note and is available to a
  seat, keep the sentence and name the gap in the PR body. Do not build a helper CLI for it.

## 1. Parser (`internal/act/verdict.go`)

- [ ] 1.1 `stripDecoration` removes leading `#`, `>`, `-`, `*`, `+`, backticks, `**`, `__`, `_` and whitespace in
      any order, then one optional `:` / `—` / `-` separator and its whitespace; `verdictNote` runs on the result.
      Verified: a table test over the nine lines recorded in the proposal, each asserting the same sub-match the
      shipped regex produced for undecorated input (`go test ./internal/act -run TestCaptureVerdictDecoration`).
- [ ] 1.2 Sha equality and `ErrUnattributableVerdict` are unchanged: a decorated `REJECT` for an older sha still
      refuses, a decorated `ACCEPT` for an older sha is still skipped.
      Verified: the existing tests in `internal/act/verdict_test.go` stay green without edits.
- [ ] 1.3 Prose stays prose: `Not ready to ACCEPT <sha> until CI is green`, `ACCEPTED <sha>` and a mid-paragraph
      mention capture nothing.
      Verified: one case per sentence in the same table.

## 2. The decision (`internal/decide/plan.go`)

- [ ] 2.1 `Intent.Audience` added; zero value is `AudienceReviewer`, so no existing caller changes behaviour.
      Verified: `go test ./internal/decide/...` green with no test edits.
- [ ] 2.2 The **no verdict** arm emits an operator intent carrying item, project, change request number and head
      sha; its event is kind `decision.no_verdict`, subject `#<item>`, detail `... head=<sha>`.
      Verified: `go test ./internal/decide -run TestNoVerdictAsksTheOperator`.
- [ ] 2.3 Dedupe by `(item, head sha)`: the same head on the next plan yields no intent; a moved head yields one.
      Verified: `go test ./internal/decide -run TestNoVerdictIsAskedOncePerHead`.
- [ ] 2.4 The **stale verdict** arm still emits `AudienceReviewer`, kind `decision.stale`, and its dedupe key now
      includes the head sha.
      Verified: `go test ./internal/decide -run TestStaleVerdictAsksTheReviewerOncePerHead`.

## 3. The writes (`internal/act/review.go`, `internal/act/tick.go`)

- [ ] 3.1 `RaiseOperatorQuestion(ctx, project, item, question, kind, headSHA, worktreeRoot)` creates the row with
      `Destination: "human@kernel"`, the body shape of `raiseAdoptionDecision` (`adopt.go:153-190`), and dedupes on
      the event ledger.
      Verified: `go test ./internal/act -run TestRaiseOperatorQuestion` — one row, destination asserted, second call
      is a no-op.
- [ ] 3.2 The question body names the item, the change request, the head sha, the board action
      (`POST /api/items/<n>/verdict`) and the `fleet: accept|reject` reply line.
      Verified: the same test asserts each string in the written body file.
- [ ] 3.3 `RaiseDecision` (reviewer) gains the brief: change request number, head sha verbatim, the literal command
      for the project's forge with the sha substituted, and the fallback sentence.
      Verified: `go test ./internal/act -run TestReviewBriefCarriesTheVerdictCommand` asserts the exact line
      `ACCEPT <sha>` appears with the head's sha.
- [ ] 3.4 `tick.go` dispatches by audience: `AudienceOperator` → `RaiseOperatorQuestion`, anything else →
      `RaiseDecision`. A permission failure from either raise stays on the existing `report.Skipped` path.
      Verified: `go test ./internal/act -run TestTickRoutesDecisionAudience`.
- [ ] 3.5 One end-to-end tick test with `forge.Fake` and a recording rig: a reviewer seat ends its row, the change
      request carries no verdict, and exactly one `human@kernel` row is created; the second tick creates nothing.
      Verified: `go test ./internal/act -run TestSilentReviewerBecomesAnOperatorQuestion`.

## 4. The spec and the docs

- [ ] 4.1 The delta covers decorated parsing, prose silence, the refusal, the operator question and its dedupe, the
      re-review audience, the brief's command, and the note-as-record rule.
      Verified: `openspec validate reviewer-verdict-contract --type change --strict` and
      `openspec validate --all --strict | tail -1` (quote the totals line in the PR).
- [ ] 4.2 `docs/` gains nothing: the verdict contract is operator-facing behaviour, and this change's own spec is
      the reference. If a doc already states the old regex, correct it there and say which file.
      Verified: `grep -rn "ACCEPT|REJECT" docs/ 2>/dev/null` empty or the corrected file named in the PR.
