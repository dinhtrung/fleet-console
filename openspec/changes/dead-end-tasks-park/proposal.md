# Proposal — a task a builder can't finish stops re-dispatching

## Why

A task a builder cannot finish is re-dispatched every few minutes, to whichever builder is free, until a
person notices. Each copy costs a builder a turn and produces nothing.

Seen in production:

- **mnpserver2 #156**, 2026-11-17: dispatched at 09:17, 09:23 and 09:26. Task 3.1 was done on `fa9eb84f` with
  no MR, because 3.2–3.4 need the owner or live infra (push an alert rule with `gcx`, prove it on live
  Prometheus).
- **traffic-simulator-v2 #480**, 2026-11-18: dispatched at 04:08, 04:11 and 04:24. It is a `[live]` Firefox
  check that needs a deployed instance.

Both loops were stopped by hand with the `onhold` label.

`internal/decide/decide.go`, in the ready-item walk, adopts any item with a branch and no change request as
unfinished work (`markReady`). A builder that ends its row blocked — a partial commit, no MR, no live row —
leaves exactly that state, so the next tick dispatches the item again. `dispatched[]` and `liveRows[]` do not
cover an ended dispatch, so nothing remembers that the last attempt already stopped here.

## What this adds

- **No re-dispatch of an unchanged dead end.** An item whose newest dispatch ended without a change request is
  not dispatched again until something changed: the branch head moved since that dispatch ended, or the owner
  released it. Instead, one operator question is raised per `(item, branch head)`, naming the branch, the head
  and the last row's resolution. Adopting a branch with no change request stays allowed when its head is new.
- **Tasks only a person can do are never dispatched.** A project label, default `manual`, is skipped exactly
  like `onhold`, so an epic can mark `[live]` or post-deploy tasks at creation time.
- **Docs.** How to mark such tasks, what the new skip and question look like, and how to release an item.

## Non-goals

- No new board surface: the dead-end question is an ordinary decision row, and the skip shows in the plan
  reasons the board already reads.
- No polling added: the head comes from the git read the tick already does, the answer from the event ledger
  the app already writes.
- No change to what ends a dispatch: reconciliation still ends an attempt when its row is terminal, absent, or
  its change request merged. The new fact is recorded at that moment, not discovered later.

## Impact

`internal/git` (the head `ls-remote` already prints), `internal/store` (one dispatch column and one ledger
read), `internal/decide` (the ready walk and the raise), `internal/act` (record the head when an attempt ends;
a dead-end brief), `cmd/fleet-console` (fold the operator's answers into the snapshot), `docs/fleet-console.md`
(the operator's guide).
