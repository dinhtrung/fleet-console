# Tasks — why the fleet is waiting

## 1. Read the attention facts

- [ ] 1.1 `internal/rig`: decode `needsAttention`, `arbitratedNeedsInput`, `needsInputEvidence` per node into a
      `SeatAttention{Seat, Rig, Kind, Question}` slice; keep the decode additive so older rigs (keys absent) still work.
- [ ] 1.2 Test: a fixture with all three keys → three entries with the right kind; a fixture without them → empty and
      no error.

## 2. One obligation per question

- [ ] 2.1 `internal/act`: an `attention` map keyed by rig, holding `seat + hash(question)` and the first tick seen,
      memory-only like `quiet`.
- [ ] 2.2 Write `attention.raised` once per fingerprint and one notice; write `attention.resolved` on the first
      **successful** read that lacks it; a failed read resolves nothing.
- [ ] 2.3 Test: five ticks one question → one event; answered → one resolution; failed read → nothing.

## 3. The capacity line names the human

- [x] 3.1 `decide.fillSeats` (or the refusal detail): when a taken seat is `attention_required`, append
      `(N waiting on a human)` to the `no_free_builder` detail.
      Shipped as `noFreeBuilderDetail` beside `fillSeats`, reading a new `Snapshot.WaitingOnHuman` seat
      list: a waiting seat is not a running seat, so it is never in `Builders` and the fact needs its own
      field. It applies to both forms that say the seats are taken — the bare line and a reached tier with
      no free seat — and not to a spill-trigger refusal, which names its own reason. Folding the list in
      from the rig read is 1.1's job — until it lands the detail is unchanged.
- [x] 3.2 Test: two admitted seats, one waiting → the detail carries the count; none waiting → unchanged string.
      `internal/decide/waiting_on_human_test.go`: the spec's exact string, the golden bare string, the
      tiered all-seats-taken repro (declared / undeclared rig), the cap-bound gate, and a spill refusal
      keeping its own wording.

## 4. The last reason survives the tick

- [x] 4.1 `internal/store`: a `plan_reason` table `(project_id, item_number, reason, detail, head, tick_at)`, upserted
      per item, deleted when the item is dispatched or leaves the project.
      Shipped as `schemaV6` plus `ReplacePlanReasons` / `PlanReasons` in `internal/store/plan_reason.go`: the write
      replaces the project's rows in one transaction (`ReplaceItems`' shape) instead of an upsert plus a prune. The
      result is the view this task asks for — one row per item, gone the same tick the item is dispatched or leaves
      the forge — because a dispatched or departed item is in no plan skip, so nothing re-inserts it. Replace also
      spares the write a set of "which items are still here" it would otherwise have to derive.
- [x] 4.2 `internal/act`/`cmd`: write it from `decide.Result.Skips` after each plan; delete for dispatched items in the
      same tick.
      Written in `act.RunTick` (`recordPlanReasons`), not `cmd`: the tick is where the plan is applied, and a write in
      `cmd` would have to re-derive the dispatched set from the intents. `head` is `skipHead` — the same head the
      `dispatch.skip` ledger key uses. A plan that did not run writes nothing (stale forge read, disabled or onhold
      project), so a failed read cannot blank the column; a store failure is one skip line and never fails a tick
      that already dispatched.
- [x] 4.3 Tests: same reason twice → one row with the later time; reason change → overwritten; dispatch → deleted.
      `internal/act/plan_reason_test.go` drives `RunTick` for all four spec scenarios plus a stale-read tick and an
      emptied project; `internal/store/plan_reason_test.go` covers the row semantics and project scoping. Red/green:
      with the write neutered all four act tests fail, and with the delete neutered the dispatch and departure tests
      fail.

## 5. Surfaces

- [x] 5.1 Project page: a "waiting on a human" panel from the seat list, with seat, rig, question, age, and a link to
      the rig UI URL. No approve control.
      Shipped as `internal/web/waiting.go` (`waitingPanel`) on `GET /api/projects`'s project shape, rendered by the
      Projects page. The seats come from the mirror's `rig_seats` snapshot (no per-request rig call); the read's health
      is the status block's `rig_stale` plus the snapshot's presence (unread is `unknown`, never clear); the age is the
      ledger's newest standing `attention.raised` (#427), with the snapshot stamp as the fallback; the link is
      `rig.Status().UIURL()`, the daemon's own port, read once and only when something waits.
- [x] 5.2 Project page: the item why-column reads the recorded reason; "no tick has run" when the table is empty.
      Shipped on the board's item table — the surface that has an item why-column: `boardItem.plan_reason`/`plan_age`
      from `store.PlanReasons` (#428) and `boardProject.planned` (a tick's status block, or any recorded reason) so an
      empty table after a real plan reads as "the last plan skipped nothing" and only a never-planned project says
      "no plan has run since start".
- [x] 5.3 Tests: panel renders with one waiting seat; unknown rig read renders "unknown", not "clear"; why-column with
      and without a row.
      `internal/web/waiting_test.go` (one seat with question+age+UI link; a failed read is unknown with the read's own
      words; a never-read project is unknown; a seat with no question text is listed as Asking; the age comes from the
      ledger's raise, not the read) and `internal/web/why_column_test.go` (recorded reason+detail+age; a planned
      project with no record; a never-planned project). `internal/rig/status_test.go` pins the UI URL read.

## 6. Docs and gate

- [x] 6.1 `docs/fleet-console.md`: what `attention_required` means, why the board does not approve, how to read the
      why-column.
      Shipped as "Why the fleet is waiting" with its three subsections. #428's half of 6.2/6.3 was verified against
      #440's base; the block's own gate is run at the rebase onto main.
- [ ] 6.2 `go build ./... && go vet ./... && go test ./...` with output pasted in the PR.
- [ ] 6.3 `openspec validate --all --strict`.
