# Tasks — a task a builder can't finish stops re-dispatching

## 1. Record the head an attempt stopped at

- [x] 1.1 `internal/git`: `Branch` carries the sha `ls-remote` prints; the fixture pins it.
- [x] 1.2 `internal/store`: schema V8 adds `dispatch.end_head`; `EndDispatchWithHead` writes it with
      `ended_at` and every dispatch read returns it. An empty head is "the end recorded no head".
- [x] 1.3 `internal/act/reconcile.go`: an attempt ended with a merged change request records no head; an end
      by terminal row or absence records the item's branch head from the same tick's git read.
- [x] 1.4 Test: `internal/act/reconcile_test.go` — the two arms, with and without an open change request.

## 2. The dead_end skip

- [x] 2.1 `internal/decide`: `ReasonDeadEnd` and the ready walk check — ended, head recorded, origin
      unchanged, no release after the end — ahead of the epic gate.
- [x] 2.2 `internal/mirror`: the snapshot carries the branch head, the attempt's end head and the row's
      resolution.
- [x] 2.3 Tests: `internal/decide/dead_end_test.go` — unchanged head parks, moved head adopts, never
      dispatched adopts, an attempt with no end head adopts, the release cases.

## 3. One operator question per (item, head)

- [x] 3.1 `internal/decide.PlanTick`: one `decision.dead_end` operator intent per dead end, with the branch,
      the head and the last row's resolution in the text.
- [x] 3.2 `internal/act`: the dead-end brief says where the attempt stopped and names the three answers;
      `RaiseOperatorQuestion`'s ledger key is the dedupe (kind, item, head).
- [x] 3.3 Tests: `internal/act/dead_end_test.go` — one row, the brief's facts, a second tick adds nothing.

## 4. Release

- [x] 4.1 `internal/rig`: the queue row decodes `resolution`.
- [x] 4.2 `internal/store.DeadEndReleases`: the raise and the operator's answer, joined through the row id;
      the answer's word decides, and the answer's time is kept.
- [x] 4.3 `cmd/fleet-console`: fold the releases into the snapshot; `decide` lets a released item through
      when the answer is later than the end, which spends the release.
- [x] 4.4 Tests: the store fold's scope and word match; the release adoption and the spent release.

## 5. The manual label (child #527)

- [x] 5.1 A project key naming the label (default `manual`) and a `manual` skip reason beside `onhold`.
      Shipped as the reserved `labels.manual` entry (`owner-only` names a custom label) with
      `config.ManualLabelFor`; the walk skips with reason `manual`, detail `owner-only task`, in `internal/decide`.
- [x] 5.2 The board shows such items as manual; a serial epic does not count a `manual` child as its holder.
      The board reads the plan's recorded reason and gives the row state `manual (owner)` with its own count;
      the holder loop and the candidate walk both exclude manual children.
- [x] 5.3 Tests: the default and a custom label, and the serial-epic slot.
      `internal/decide/manual_test.go`, `internal/web/manual_board_test.go`, `internal/config/config_test.go`.

## 6. Documentation (child #528)

- [x] 6.1 How to mark owner-only tasks, and how `manual` differs from `onhold`.
      `docs/fleet-console.md` → "Tasks the fleet should not take": the `gh`/`glab` example, the board's
      `manual (owner)` row, the `labels.manual` rename, and the pause-vs-person contrast; the README's project
      key list names the reserved key.
- [x] 6.2 The `dead_end` skip and its question, with the three answers.
      `docs/fleet-console.md` → "When a branch stops where it started": the skip line, the one question per
      (item, head) with the last row's resolution, and what release / hold / hand-to-a-person each look like.
- [x] 6.3 How to release an item: push a new head, or answer the question with `release`.
      "Releasing an item": the two paths, the board-only answer caveat for the CLI, and the one-attempt rule
      for a ledger release.
