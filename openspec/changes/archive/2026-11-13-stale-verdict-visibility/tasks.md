# Tasks — a refusal is recorded once, explained, and never stalls silently

A box is ticked only with the command that proved it, quoted in the pull request. Epic: GitHub
`dinhtrung/fleet-console#309`; the code tasks are its children #311–#315.

## 0. Ground rules — fixed vocabulary

**Error identities** (`internal/act/verdict.go`):
`UnattributedRejectError{NoteID, Written, Head string}` — the newest verdict-shaped note is a REJECT whose sha is
not the head. `UnattributableVerdictError{NoteID, Line, Reason}` keeps the parse failure. Both answer
`Is(ErrUnattributableVerdict) == true`. The callers distinguish them with `errors.As`, never by parsing `Error()`.

**Event kinds:**
- `verdict.unattributed` — existing; now written for a rejection too.
- `change_request.no_item` — new; a change request whose body closes no item.
- `decision.stall` — new; a stale review that outlived its nudges.
- `change_request.ready` — existing; unchanged.

**Detail grammars (the trailing `;` on a dedupe key is the terminator, so `12` never matches `123`):**
- `verdict.unattributed`: subject `#<item>`, detail `note=<id>; sha=<written>; head=<head>`, key `note=<id>;`.
- `change_request.no_item`: subject `#<item>` when the title or branch names one, else `cr-<number>`, detail
  `cr=<n>; head=<sha>; named=<n|none>;`, key `cr=<n>; head=<sha>;`.
- `decision.stall`: subject `#<item>`, detail `head=<sha>; row <qitem>; age=<minutes>m; nudges=<n>`, key inherited
  from `RaiseOperatorQuestion` (`head=<sha>;`).

**Skip lines** (one per record, printed on the tick that wrote it):
- `#<item> verdict: <error text>` — unchanged shape, printed once per note.
- `#<item> change request #<cr>: names #<item> only in the title/branch; the gate reads "Closes #<n>" in the body`
  when an item was named elsewhere, and `change request #<cr>: body closes no item` when nothing was.

**Threshold:** `StaleEscalateAfter = 45 * time.Minute` in `internal/decide/plan.go`, beside `RenudgeAfter` (15 m) and
`RenudgeCooldown` (30 m). Not per-project config.

**Brief sentence** (`## Publish`, `internal/act/actions.go`): the gate finds the item only through `Closes #<item>`
in the body; a body that only references the item (`Refs`, `Part of`, a title reference) is invisible to it. If the
item cannot be finished in this attempt, do not open a change request that only references it — hand off with the
candidate sha, the commands and what remains, so the coordinator splits the item into sub-issues.

**Diagnostic only:** `forge.ChangeRequest.Title` (GitLab `title`, GitHub `title`) may be read by the
`change_request.no_item` diagnostic and by nothing else. `Merge`, `MergeAdopted`, `CaptureVerdict` and `decide` never
read it, and no branch-parsing fallback may associate a change request with an item.

**Build gate:** `go build ./... && go vet ./... && go test ./...`; `openspec validate --all --strict`. Frontend gates
only if `src/` changed (it does not in this change).

## 1. One record per unattributable verdict — issue #311

- [x] 1.1 Add `UnattributedRejectError` with the family identity; return it from the sha-mismatch branch only
      (`verdict.go:182-185`), leaving the parse-failure branch untouched. Verify:
      `go test ./internal/act -run 'TestUnattributableRejectIdentity|TestParseFailureKeepsItsType' -v`.
- [x] 1.2 One helper writes `verdict.unattributed` once per note and reports whether it wrote; both the ACCEPT and
      the REJECT arm call it. Verify: `go test ./internal/act -run 'TestUnattributedAcceptIsAuditedOnce|TestUnattributableRejectIsAuditedOnce' -v`.
- [x] 1.3 `report.Skipped` carries the line only on the tick that wrote the row. Verify:
      `go test ./internal/act -run 'TestUnattributableRejectPrintsOnce|TestUnattributableRejectNewHeadPrintsAgain' -v`
      — five ticks over the #455 shape → one line, one event.
- [x] 1.4 A failed ledger write keeps today's line and never fails the tick. Verify:
      `go test ./internal/act -run 'TestUnattributableRejectLedgerFailureStillPrints' -v`.

## 2. The refusal explains itself — issue #312

- [x] 2.1 Replace the reason with the order-and-unblock sentence fixed in §0; keep `NoteID` and the quoted line.
      Verify: `go test ./internal/act -run TestUnattributableRejectText -v`, quoting the rendered #455 line.
- [x] 2.2 Assert the text contains no timestamp and that `forge.ChangeRequest` gained no field. Verify:
      `git diff --stat internal/forge` in the pull request body.

## 3. A stalled review asks a human — issue #313

- [x] 3.1 Add `StaleEscalateAfter` and the plan predicate: live row, text names the current head, `CreatedAt` older
      than the threshold, `LastNudgeAt` non-zero. Verify:
      `go test ./internal/decide -run 'TestStaleStallAsksOnce|TestStaleStallWithoutNudgeIsQuiet|TestStaleStallAnsweredBeforeThresholdIsQuiet' -v`.
- [x] 3.2 Emit `IntentRaiseDecision` with `AudienceOperator`, kind `decision.stall`, `HeadSHA` and `ChangeRequest`
      set; no new transport, no config field. Verify:
      `go test ./internal/decide -run TestStaleStallNewHeadAsksAgain -v` and
      `go test ./internal/act -run 'TestRaiseOperatorQuestionDedupesByHead' -v`.

## 4. A change request that closes nothing is visible — issue #314

- [x] 4.1 Make the `continue` at `tick.go:129-135` report: one line per `(change request, head)`, with the item named
      when the title or `feature/<n>-` names one. Verify:
      `go test ./internal/act -run 'TestNoClosingItemIsVisible|TestNoClosingItemNamesTitleOrBranch|TestNoClosingItemPrintsOnce' -v`.
- [x] 4.2 Write `change_request.no_item` with the key in §0; a ledger failure never fails the tick. Verify:
      `go test ./internal/act -run TestNoClosingItemEventOnce -v`.
- [x] 4.3 Add `Title` to `forge.ChangeRequest` and map it in both adapters; assert nothing in the merge path reads
      it. Verify: `go test ./internal/forge ./internal/act ./internal/decide -v` and
      `go test ./internal/act -run TestRefsChangeRequestNeverMerges -v`.

## 5. The brief gives partial work a lawful move — issue #315

- [x] 5.1 Add the two sentences fixed in §0 to `## Publish`; change nothing else in the template. Verify:
      `go test ./internal/act -run TestBriefPublishContract -v`, quoting the rendered `## Publish`.
- [x] 5.2 If `docs/fleet-console.md` restates the publish contract, align that paragraph. Verify: `git diff docs/`
      in the pull request body.

## 6. After the code lands

- [x] 6.1 Replay the two measured cases from fixtures: #455 (reject on the parent, five ticks, then `ACCEPT` at the
      head) and #683 (`Refs` body, title and branch naming the item, ten ticks). Quote the journal lines and the
      event rows.
- [x] 6.2 Archive this change set with `openspec archive stale-verdict-visibility` once every box above is ticked,
      and tick the epic's acceptance list.
