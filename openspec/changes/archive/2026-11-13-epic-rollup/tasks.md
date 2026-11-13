# Tasks — epic rollup

## 1. The rule (`internal/act/rollup.go`)

- [x] 1.1 `func Rollup(parent *int, items []forge.Item) (Decision, error)` — pure, no forge, no store: returns
      `Close` with the child count, or `Skip` with the reason (`no parent`, `parent not an epic`,
      `N sub-issues still open (#a, #b)`, `no sub-issues in the snapshot`, `epic already closed`).
- [x] 1.2 Children are the items whose `Parent` equals the parent number; both open and closed are counted so the
      "no sub-issues" case cannot be confused with "all closed".
- [x] 1.3 Table-driven tests: all closed / one open / none in snapshot / parent not epic / no parent / parent closed.

## 2. The write (`internal/act/tick.go`, after `Finalize`)

- [x] 2.1 Call `Rollup` once per merged item, using the same `in.Items` snapshot the plan used; no extra forge read.
- [x] 2.2 On `Close`: `Forge.Comment(parent, …)` naming the item, the pull request and "every sub-issue is closed",
      then `Forge.CloseItem(parent)`, then one event `epic.closed` (`#<epic>`, `N sub-issues`).
- [x] 2.3 On `Skip`: append `#<epic> rollup: <reason>` to `report.Skipped`, so the journal names the reason instead of
      the step disappearing.
- [x] 2.4 On a forge error in comment/close: also `report.Skipped`, and never touch the merge result — the merge is
      the fact, the rollup is a courtesy.
- [x] 2.5 One close per tick per epic even when two merged items share the parent (a map of epics already closed).

## 3. Proof

- [x] 3.1 Tick-level test with `forge.Fake`: merge closes the last child of a two-child epic → fake records
      `comment(epic)` then `close(epic)` and the report carries the `epic.closed` event.
- [x] 3.2 Same fixture with one child open → no writes to the epic, skip line present.
- [x] 3.3 Same fixture with the epic absent from `in.Items` → no writes (snapshot guard).
- [x] 3.4 `CGO_ENABLED=0 go build ./... && go test ./...` green; the tests quote the fake's call log as evidence in
      the pull request.

## 4. After it lands

- [x] 4.1 Close epics #64 and #68 by hand once (they predate the feature) with a comment saying the rollup exists now.
      Done 2026-10-30T20:12Z, before this change landed, and verified 2026-11-01: both are `CLOSED`, and each closing
      comment says why the app did not do it and that the rollup fixes it — #68 names "the rollup that fixes that is
      specified in `openspec/changes/epic-rollup` and tracked by epic #104 (issues #105, #106, #107)"; #64 says
      "Epic #104 (`openspec/changes/epic-rollup`) makes the app do it from now on". `gh issue view 64 68 --json
      state,closedAt` → `CLOSED 2026-10-30T20:12:45Z` and `CLOSED 2026-10-30T20:12:42Z`.
