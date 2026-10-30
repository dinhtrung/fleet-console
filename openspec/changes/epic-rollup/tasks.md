# Tasks — epic rollup

## 1. The rule (`internal/act/rollup.go`)

- [x] 1.1 `func Rollup(parent *int, items []forge.Item) (Decision, error)` — pure, no forge, no store: returns
      `Close` with the child count, or `Skip` with the reason (`no parent`, `parent not an epic`,
      `N sub-issues still open (#a, #b)`, `no sub-issues in the snapshot`, `epic already closed`).
- [x] 1.2 Children are the items whose `Parent` equals the parent number; both open and closed are counted so the
      "no sub-issues" case cannot be confused with "all closed".
- [x] 1.3 Table-driven tests: all closed / one open / none in snapshot / parent not epic / no parent / parent closed.

## 2. The write (`internal/act/tick.go`, after `Finalize`)

- [ ] 2.1 Call `Rollup` once per merged item, using the same `in.Items` snapshot the plan used; no extra forge read.
- [ ] 2.2 On `Close`: `Forge.Comment(parent, …)` naming the item, the pull request and "every sub-issue is closed",
      then `Forge.CloseItem(parent)`, then one event `epic.closed` (`#<epic>`, `N sub-issues`).
- [ ] 2.3 On `Skip`: append `#<epic> rollup: <reason>` to `report.Skipped`, so the journal names the reason instead of
      the step disappearing.
- [ ] 2.4 On a forge error in comment/close: also `report.Skipped`, and never touch the merge result — the merge is
      the fact, the rollup is a courtesy.
- [ ] 2.5 One close per tick per epic even when two merged items share the parent (a map of epics already closed).

## 3. Proof

- [ ] 3.1 Tick-level test with `forge.Fake`: merge closes the last child of a two-child epic → fake records
      `comment(epic)` then `close(epic)` and the report carries the `epic.closed` event.
- [ ] 3.2 Same fixture with one child open → no writes to the epic, skip line present.
- [ ] 3.3 Same fixture with the epic absent from `in.Items` → no writes (snapshot guard).
- [ ] 3.4 `CGO_ENABLED=0 go build ./... && go test ./...` green; the tests quote the fake's call log as evidence in
      the pull request.

## 4. After it lands

- [ ] 4.1 Close epics #64 and #68 by hand once (they predate the feature) with a comment saying the rollup exists now.
