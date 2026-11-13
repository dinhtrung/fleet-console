# Proposal — an item labelled an epic is an epic on the first read

## Why

On 2026-11-05 an epic was created and its thirteen children linked two minutes later. The mirror read the epic while
`sub_issues_summary.total` was still 0, so `Items` returned it as `Kind: task` (`internal/forge/github.go:76`), the
plan saw one ready item, and the app dispatched it: a builder got a worktree, a branch and a queue row for an issue
whose body was a plan. The row had to be cancelled by hand and the label the dispatch wrote
(`status::ongoing`) removed by hand.

The existing guard is not wrong — `Items` already uses `sub_issues_summary` when the forge sends it and only walks
`sub_issues` for epics, so a task costs no extra call. The gap is **timing**: between `gh issue create` for an epic
and the first `addSubIssue`, no forge field distinguishes it from a task. With `act` on and a 60-second tick, that
window is a live race every time an epic is written by hand.

The one fact available at creation time is the label the operator already puts on it. Every epic in this repository
carries `type::epic` from the moment `gh issue create` returns.

## What changes

- `Items` treats an item carrying the epic label as `KindEpic` even when it has no sub-issues, and still reads its
  `sub_issues` so its children get their `Parent` set on that same read.
- The label is one exported constant in `internal/forge`, next to the adapter that reads it, and is documented as
  the operator's contract: label the container when you create it.

## Losses, named

- **A second signal for the same fact.** Kind now comes from the summary, the child list, or the label, and they can
  disagree — an item labelled `type::epic` with real children is still an epic, so the disagreement is benign; a
  *task* mislabelled `type::epic` is skipped as a container, which is visible on the board as `epic` and is the
  operator's mistake to fix.
- **GitLab is untouched.** Its adapter reads `issue_type` from the work-item hierarchy, which is set at creation;
  there is no equivalent window. Adding a label path there would be a second mechanism with no failure to prevent.

## Capabilities

### New Capabilities

- `epic-detection`: which facts make an item an epic, the label's role, and the rule that reading a task costs no
  sub-issue call.

## Impact

- `internal/forge/forge.go` — one constant.
- `internal/forge/github.go` — the label check in the item loop; no change to the phase-1 selection.
- `internal/forge/forge_test.go` + a fixture — the new case.
- `docs/my-stack.md` — one line: an epic is labelled when it is created.
