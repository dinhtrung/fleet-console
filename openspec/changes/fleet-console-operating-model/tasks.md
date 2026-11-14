# Tasks — operating model

Ordered so each task leaves the tree green. `#n` is the GitHub issue that carries it.

## 1. One writer (D1, #44)

- [ ] 1.1 `store.Open` takes `<database>.lock` with `flock`; a held lock returns `another instance owns <db>`
- [ ] 1.2 `cmd/fleet-console` exits with code 3 on a held lock, before logging a config
- [ ] 1.3 Test: two opens on one file, second fails; a killed holder releases without a timeout

## 2. Stale verdict (D2, #45)

- [ ] 2.1 `decide` compares the newest verdict sha with the head and emits `verdict stale` with both shas
- [ ] 2.2 The act loop raises one report row per (item, head) to the reviewer of the building rig
- [x] 2.3 Board and drawer render `verdict stale`; `parked` keeps its meaning
- [ ] 2.4 Tests: moved head raises once; a second tick raises nothing; an accept on the new head clears it

## 3. Disabled projects (D3, #46)

- [ ] 3.1 `--apply-config` sets `enabled = 0` for absent projects and prints `disabled <key>`
- [ ] 3.2 `--prune` deletes only disabled projects with no live dispatch, and refuses otherwise
- [ ] 3.3 Tests: remove → disabled, no intent; re-add → same id; prune with a live row → refused

## 4. Rig wins (D4, #47)

- [ ] 4.1 Reconcile before planning: `superseded`, `merged`, `abandoned`, one event each
- [ ] 4.2 No dispatch in a tick that reconciled an abandonment
- [ ] 4.3 Tests: the three transitions, each asserting the board state and the event

## 5. Two-phase dispatch (D6, #49)

- [ ] 5.1 `CreateDispatch(pending)` before `QueueCreate`, then stamp `qitem_id`
- [ ] 5.2 Boot adoption: one match → stamp; none or many → `abandoned` + one decision
- [ ] 5.3 Tests: crash between row and record; two matches; no match

## 6. Tiers (D5a/D5b, #48 — rewritten for this change)

> 6.4–6.7 and 7.1–7.2 were removed with their requirements (#398): no project runs tiers, and a second rig
> on one project is forbidden by the operating model. Only fill-order and `on-full` spilling remain.

- [ ] 6.1 Config: `rigs: [{name, tier, spill}]`, `rig: name` as the one-element form, `policy: tiered`
- [ ] 6.2 Capacity summed over the active tier; a rig named twice or by two projects fails validation
- [ ] 6.3 Fill-first inside a tier; a boundary is crossed only by its trigger
      not fail over; re-dispatch reuses the branch

## 7. Verify on the box

- [ ] 7.3 Confirm a second app instance refuses to start while the service runs
