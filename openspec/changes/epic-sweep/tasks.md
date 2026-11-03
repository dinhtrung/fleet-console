# Tasks — epic sweep

A box is ticked only with the command that proved it, quoted in the pull request. Tasks 1–2 are #123 (spec and
decision); task 3 is #124 (the write).

## 1. The decision (`internal/act/sweep.go`) — #123

- [x] 1.1 `func Sweep(epic forge.Item, items []forge.Item) Decision` beside `Rollup`: pure, no clock, no forge, no
      store. Candidate row and read in, close or skip with a reason out. Reuses `forge.Item` and `forge.KindEpic`;
      no parallel item model. Verified: `go test ./internal/act -run 'TestSweep|TestSweepIsPure'`.
- [x] 1.2 The checks in operator order — not an epic (quiet), already closed (quiet), children counted by `Parent`
      with the epic's own row excluded, zero children (journaled), open child (quiet, numbers sorted), otherwise
      close. Verified: the same run, one case per check.
- [x] 1.3 `Decision.Quiet` added in `rollup.go`, set only by `Sweep`; `Rollup` leaves it false so the merge path's
      journaling is unchanged. Verified: `go test ./internal/act -run TestRollup` still green.
- [x] 1.4 `Rollup` and `rollupAfterMerge` untouched apart from the `Decision` field. Verified: `go test ./internal/act/...`.

## 2. The spec — #123

- [x] 2.1 `openspec/changes/epic-sweep/` — proposal, design (the trap #122 names, the read #124 inherits, the rule
      in order, attribution, the failure path), this file, and the delta in `specs/epic-sweep/spec.md`.
      Verified: `openspec validate epic-sweep --strict`.
- [x] 2.2 The delta covers closing, the zero-children guard, the open-child silence, already-closed and non-epic
      rows, purity plus attribution, and the failure path. Verified: `openspec validate epic-sweep --strict` and the
      scenario count in the validation output.

## 3. The write (`internal/act/tick.go`) — #124

- [ ] 3.1 After the merge loop, for each open epic in the tick's snapshot, read its sub-issues (open and closed)
      through the forge and pass the read to `Sweep`. The tick's open-only snapshot is not the evidence.
- [ ] 3.2 On close: comment naming the sweep as the cause, `CloseItem`, then one `epic.closed` event with detail
      `sweep: <N> sub-issues`.
- [ ] 3.3 Idempotent within a tick and across ticks: one close per epic per tick, no comment and no event for an
      epic already closed.
- [ ] 3.4 A failing read, comment or close appends `#<epic> sweep: <error>` to `report.Skipped`; the tick still
      succeeds and the next tick retries.
- [ ] 3.5 The forge reads are bounded to one sub-issue read per open epic per tick and the count is visible in the
      tick report where a counter already exists.
- [ ] 3.6 Tick tests with `forge.Fake`: zero merges closing one completed epic and leaving an incomplete one alone;
      an empty read closing nothing; a comment error leaving the epic open with a skip line; two completed epics
      producing two closes and two events. Extend the existing tick tests.
