# Design — the label is the only fact available before the first link

## D1. Detect at the earliest moment the forge offers

`sub_issues_summary.total` is authoritative once children exist, and the adapter already uses it (and skips the
per-item `sub_issues` call when a present summary says zero — that saving stays). It says nothing useful in the two
minutes before the first `addSubIssue`. The label is present on the first read, is written by the same command that
creates the item, and is already how a human distinguishes an epic in this repository's own issue list.

So the rule is: epic if the summary says so, **or** the child list says so, **or** the label says so. Three
independent facts, one classification, and the classification is only wrong if a human mislabels a task.

Rejected: waiting N ticks before dispatching a fresh issue. It trades one race for a latency floor on every task,
and nothing in the plan could distinguish "fresh, maybe an epic" from "fresh, just opened".

Rejected: refusing to dispatch any item whose body looks like a plan. That is a heuristic on prose.

## D2. The label lives in the adapter, as a constant

`internal/decide` owns `MarkerLabel = "status::ongoing"` because the act loop writes it. The mirror owns the read,
so `forge.EpicLabel = "type::epic"` lives beside the adapter that reads it. A config key (`labels.epic`) exists as
a shape — `config.Project.Labels map[string]string` — but it is not passed to any adapter today, and adding a
project-specific label name would let a project's config change the meaning of an issue type. One constant, one
meaning.

## D3. A label-epic still reads its children

Phase 1 selects the items whose `sub_issues` are fetched with `hasSummary[n] && !epics[n]` → skip. A label-epic has
`epics[n] = true`, so it is fetched: its children (created before or after this read) get their `Parent` set on the
same tick, which is what makes the dispatcher wait on the epic's children rather than run them as orphans.

The cost is one `sub_issues` call per label-epic per tick — the same call an epic with a non-zero summary already
costs.

## D4. What the plan does with it

Nothing new: `Decide` already skips `KindEpic` with `ReasonEpic`. The observable change is that the skip happens on
the first read instead of two minutes later — and the board's row for the epic reads as a container from the start.

## D5. The test is the fixture that failed

A fixture with one issue labelled `type::epic`, `sub_issues_summary.total = 0`, and no `sub_issues` entry in the
exact map (the runner answers `[]` for calls it does not know) must come back `KindEpic`, and a second unlabelled
issue must come back `KindTask` with no `sub_issues` call recorded for it. That is the race, reduced to a table.
