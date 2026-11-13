# Tasks — read the verdict the reviewer actually wrote

## 1. The text scan

- [x] 1.1 `internal/act/verdict_text.go`: `verdictText` (fences, comments, 1024-rune cut),
      `normalizeVerdictLine` (link → emphasis → non-letters → one label), `scanVerdictText` (line-start verdicts,
      both-polarity and negation questions, sha by line then window, short sha by prefix of the head).
      Proof: `go test ./internal/act -run TestScanVerdictText` — 15 cases, pass.
- [x] 1.2 Short sha: 4–6 characters captured only as a prefix of the head; anything else skipped, not questioned.
      Proof: `TestScanVerdictText/short_sha_that_prefixes_the_head`, `/short_sha_that_names_nothing`, pass.

## 2. Capture

- [x] 2.1 `CaptureVerdict` keeps the anchored first-line grammar as pass one, runs the scan as pass two, and refuses
      to capture a first line whose note argues with itself.
      Proof: `go test ./internal/act -run 'TestVerdictCapture|TestCaptureVerdictDecoration'` — pass, including
      `prose accept is not a verdict` (the #264 guard) and the new `qa label`, `icon and bold`, `link text`,
      `label on its own line, sha below`, `both polarities`, `a negated verdict` cases.
- [x] 2.2 A first line that opens with a verdict word and names no parsable sha keeps the
      `UnattributableVerdictError` refusal (the audit keeps the typo) and the tick raises the question for it; a
      *wrapped* verdict with no sha returns `UnresolvedVerdictError` directly.
      Proof: `TestVerdictCapture/rejection_with_no_sha_refuses`,
      `TestRunTickAsksOperatorForUnresolvedVerdict`, `TestMalformedVerdictRecordedOncePrintsOnce` — pass.

## 3. The question

- [x] 3.1 `RaiseVerdictQuestion` writes the brief (note, head, written sha, reason, the line) with the three
      answers, one row per `(item, note, head)`, destination `operatorDestination(mode, seat)`, falling back once
      to `human@kernel` when the seat cannot take the row.
- [x] 3.2 The tick raises the question for every capture error it cannot resolve: ambiguous scan, malformed first
      line, accept or reject at another sha.
      Proof: `TestRunTickAsksOperatorForUnresolvedVerdict` — one row for `QA ACCEPT`, no merge, second tick raises
      nothing (ledger dedupe), destination `human@kernel` without a relay.

## 4. Docs and configuration

- [x] 4.1 `docs/fleet-console.md` — "Reading a verdict" rewritten: the line-start rule, the closed label list, the
      short-sha prefix rule, the question and its dedupe.
- [x] 4.2 `docs/fleet-console.md` carries the `verdict_relay` block, the seat spelling (dots become dashes) and
      what `scribe` and an absent block mean.
- [ ] 4.3 homebox: set the block for `fleet-console` and `tsim`, restart the service, confirm a question lands on
      the seat and not `human@kernel` (journal line names the row and its destination).

## 5. Gate

- [ ] 5.1 `go build ./... && go vet ./... && go test ./...` green except the two known environment failures
      (`culture.TestGuardRefusesSymlink` on a symlinked home, `mirror.TestLoopWakeRunsTickBeforeTimerAndResetsCadence`).
- [ ] 5.2 `openspec validate --all --strict` green.
- [ ] 5.3 PR to `main` with this change set; the merge gate of this repo is a hand merge until 4.3 is done.
