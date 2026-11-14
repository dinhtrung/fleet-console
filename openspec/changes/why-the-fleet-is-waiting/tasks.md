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

- [ ] 3.1 `decide.fillSeats` (or the refusal detail): when a taken seat is `attention_required`, append
      `(N waiting on a human)` to the `no_free_builder` detail.
- [ ] 3.2 Test: two admitted seats, one waiting → the detail carries the count; none waiting → unchanged string.

## 4. The last reason survives the tick

- [ ] 4.1 `internal/store`: a `plan_reason` table `(project_id, item_number, reason, detail, head, tick_at)`, upserted
      per item, deleted when the item is dispatched or leaves the project.
- [ ] 4.2 `internal/act`/`cmd`: write it from `decide.Result.Skips` after each plan; delete for dispatched items in the
      same tick.
- [ ] 4.3 Tests: same reason twice → one row with the later time; reason change → overwritten; dispatch → deleted.

## 5. Surfaces

- [ ] 5.1 Project page: a "waiting on a human" panel from the seat list, with seat, rig, question, age, and a link to
      the rig UI URL. No approve control.
- [ ] 5.2 Project page: the item why-column reads the recorded reason; "no tick has run" when the table is empty.
- [ ] 5.3 Tests: panel renders with one waiting seat; unknown rig read renders "unknown", not "clear"; why-column with
      and without a row.

## 6. Docs and gate

- [ ] 6.1 `docs/fleet-console.md`: what `attention_required` means, why the board does not approve, how to read the
      why-column.
- [ ] 6.2 `go build ./... && go vet ./... && go test ./...` with output pasted in the PR.
- [ ] 6.3 `openspec validate --all --strict`.
