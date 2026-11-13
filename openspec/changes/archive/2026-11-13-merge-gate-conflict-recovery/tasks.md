# Tasks — a head that cannot merge is never reviewed, and a refusal is never invented

A box is ticked only with the command that proved it, quoted in the pull request. Epic: GitHub
`dinhtrung/fleet-console#341`; the code tasks are its children #338, #339 and #340.

## 0. Ground rules

- Gate: `go build ./... && go vet ./... && go test ./...`; `openspec validate --all --strict`. Frontend gates only if
  `src/` changed (it does not). Paste the real output.
- Read `proposal.md`, `design.md` and `specs/act-merge/spec.md` before the first line of code. The spec is the
  contract; the design's `Rejected:` rows are decisions, not suggestions.
- **Land in order: #338, then #339, then #340.** They touch overlapping files (`internal/forge`, `internal/act`,
  `internal/decide`), and the sibling-task conflict that produced this change set is the reason. Each branch is
  rebased on `main` immediately before its gate run.
- **A task brief SHALL NOT quote a function's shape without naming the task that may rewrite it.** T3 of epic #309 was
  written against the pre-T2 code, T2 landed first, and the pull request went `CONFLICTING` on a head that could
  never merge.
- Do not weaken an existing refusal to make a path pass. If a real pull request trips a guard, that is the guard
  working: report it.

## 1. Mergeability has three states — issue #338

- [ ] 1.1 `internal/forge/forge.go`: add `MergeUnknown() bool` — true for `""`, `unknown`, `checking`; keep
      `MergeConflict()` as it is. Verify: `go test ./internal/forge -run 'MergeUnknown|MergeConflict' -v`.
- [ ] 1.2 `internal/forge/github.go`: decode `mergeable_state` in the single-PR refetch and carry the refetch's word,
      not the list page's. Verify: `go test ./internal/forge -run TestGitHubMergeStateComesFromTheComputingRead -v`.
- [ ] 1.3 `internal/act/tick.go`: before `Merge`/`MergeAdopted`, a `MergeUnknown()` head reports
      `#<item> merge waiting: the forge has not computed mergeability`, raises no decision, records no blocked
      merge. Verify: `go test ./internal/act -run TestUnknownMergeabilityWaitsWithoutRepair -v`.
- [ ] 1.4 Prove no repair path is reachable with an unknown word:
      `go test ./internal/act -run 'TestRaiseMergeRepairCarriesEvidence|TestUnknownMergeability' -v`.

## 2. Conflict is a planning input — issue #339

- [ ] 2.1 `internal/decide/plan.go`: when the open change request's `MergeConflict()` is true, raise
      `decision.merge_repair` and skip `decision.no_verdict`. Verify:
      `go test ./internal/decide -run TestPlanRaisesRepairInsteadOfNoVerdictOnConflict -v`.
- [ ] 2.2 Same function: skip `decision.stale` for a conflicting head, including the case where the newest verdict
      names the head (today nothing is raised there and the merge gate refuses later). Verify:
      `go test ./internal/decide -run 'TestPlanRaisesRepairInsteadOfStaleOnConflict|TestPlanStaleVerdict' -v`.
- [ ] 2.3 Keep the repair text, brief and `(item, head)` key byte-identical; the intent's kind is the only new fact
      the act loop sees. Verify: `go test ./internal/act -run TestRaiseMergeRepairCarriesEvidence -v`.

## 3. The repair row is reconciled — issue #340

- [ ] 3.1 `internal/act/repair.go`: read the newest `decision.merge_repair` event for `#<item>`, `QueueShow` the row it
      names, and treat a non-terminal row as the open one. An open row naming another head gets
      `QueueUpdate --note <new head>` plus the event for the new head — never a second row. Verify:
      `go test ./internal/act -run TestMergeRepairNotesANewHeadOnTheOpenRow -v`.
- [ ] 3.2 `internal/act/tick.go`: on every applied tick, resolve an open repair row whose premise is gone — item
      closed, change request closed or merged, or the current head green with an ACCEPT — through `ResolveRow`, with
      a note naming the fact that ended it. Verify:
      `go test ./internal/act -run TestMergeRepairResolvesWhenTheItemMerges -v`.
- [ ] 3.3 A terminal row does not silence a later conflict at a new head; the `(item, head)` key still prevents a
      re-raise at the same head. Verify:
      `go test ./internal/act -run TestMergeRepairRaisesAgainAfterResolution -v`.
- [ ] 3.4 A rig that refuses `queue update` or `queue resolve` never fails the tick: the skip line carries the
      refusal and the next tick retries. Verify: `go test ./internal/act -run TestMergeRepairReconcileFailureKeepsTheTick -v`.

## 4. Docs and close-out

- [ ] 4.1 `docs/fleet-console.md`: the merge-repair paragraph states the reconciliation rule — one open row per item,
      resolved when the premise disappears.
- [ ] 4.2 `openspec validate merge-gate-conflict-recovery --strict` and `openspec validate --all --strict` both green;
      quote the totals.
- [ ] 4.3 Archive this change set only after #338, #339 and #340 are merged and the replay below passes on `main`.
- [ ] 4.4 **Replay of the incident**: with mergeability unknown, the tick prints the waiting line and raises no row;
      with `dirty`, it raises one repair row and no review; after the rebase merges, that row is resolved and a
      second head cannot raise a duplicate.
