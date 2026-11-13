# Tasks — the epic label

## 0. Ground rules

- Gate: `go build ./... && go vet ./... && go test ./...` and `openspec validate --all --strict`. Real output.
- Fixed vocabulary: `forge.EpicLabel = "type::epic"`; no new config key, no new CLI flag.
- Do not change the phase-1 selection rule: an item with a present zero summary and no epic label still costs no
  sub-issue call.

## 1. Adapter and test

- [x] 1.1 `internal/forge/forge.go`: `const EpicLabel = "type::epic"`.
- [x] 1.2 `internal/forge/github.go`: in the item loop, after the labels are built, mark the item `KindEpic` and
      `epics[number] = true` when any label equals `EpicLabel`. Leave the summary logic and phase 1 untouched.
      Evidence: a comment on the constant naming the race it closes.
- [x] 1.3 `internal/forge/forge_test.go`: a fixture in `t.TempDir()` with two issues — one labelled `type::epic`
      with `sub_issues_summary.total: 0`, one unlabelled — asserting `epic`/`task`, that the epic's `sub_issues`
      call was made once, and that no `sub_issues` call was made for the task.
- [x] 1.4 `docs/my-stack.md`: one line — an epic is labelled `type::epic` when it is created, because that label is
      what keeps the first read from dispatching it.
