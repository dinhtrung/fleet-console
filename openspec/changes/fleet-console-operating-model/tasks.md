# Tasks — operating model

Ordered so each task leaves the tree green. `#n` is the GitHub issue that carries it.

## 1. One writer (D1, #44)

- [x] 1.1 `store.Open` takes `<database>.lock` with `flock`; a held lock returns `another instance owns <db>`
- [x] 1.2 `cmd/fleet-console` exits with code 3 on a held lock, before logging a config
- [x] 1.3 Test: two opens on one file, second fails; a killed holder releases without a timeout

## 2. Stale verdict (D2, #45)

- [x] 2.1 `decide` compares the newest verdict sha with the head and emits `verdict stale` with both shas
- [x] 2.2 The act loop raises one report row per (item, head) to the reviewer of the building rig
- [x] 2.3 Board and drawer render `verdict stale`; `parked` keeps its meaning
- [x] 2.4 Tests: moved head raises once; a second tick raises nothing; an accept on the new head clears it

## 3. Disabled projects (D3, #46)

- [x] 3.1 `--apply-config` sets `enabled = 0` for absent projects and prints `disabled <key>`
- [x] 3.2 `--prune` deletes only disabled projects with no live dispatch, and refuses otherwise
- [x] 3.3 Tests: remove → disabled, no intent; re-add → same id; prune with a live row → refused

## 4. Rig wins (D4, #47)

- [x] 4.1 Reconcile before planning: `superseded`, `merged`, `abandoned`, one event each
- [x] 4.2 No dispatch in a tick that reconciled an abandonment
- [x] 4.3 Tests: the three transitions, each asserting the board state and the event

## 5. Two-phase dispatch (D6, #49)

- [x] 5.1 `CreateDispatch(pending)` before `QueueCreate`, then stamp `qitem_id`
- [x] 5.2 Boot adoption: one match → stamp; none or many → `abandoned` + one decision
- [x] 5.3 Tests: crash between row and record; two matches; no match

## 6. Tiers (D5a/D5b, #48 — rewritten for this change)

> 6.4–6.7 and 7.1–7.2 were removed with their requirements (#398): no project runs tiers, and a second rig
> on one project is forbidden by the operating model. Only fill-order and `on-full` spilling remain.

- [x] 6.1 Config: `rigs: [{name, tier, spill}]`, `rig: name` as the one-element form, `policy: tiered`
- [x] 6.2 Capacity summed over the active tier; a rig named twice or by two projects fails validation
- [x] 6.3 Fill-first inside a tier; a boundary is crossed only by its trigger
      not fail over; re-dispatch reuses the branch
- [x] 6.8 "One unreadable rig does not blank the project" (#438): a failed fleet-wide roster read is retried per declared
      rig, so the rigs that answer keep their seats and only the blind ones are dropped. `ProjectResult.RigUnread` /
      `RigUnknown` / `RigError` carry it per project; a project whose every rig is unread writes no roster and plans
      nothing (`decide.StatusStale`, never "no free builder"), and a project with a free seat missing names the rig in
      its refusal (`…; rig <name> unread`). Verified: `TestOneUnreadRigDoesNotBlankTheOther`,
      `TestEveryRigUnreadMakesTheProjectUnknown`, `TestUnreadRigDoesNotTakeTheOtherRigsCapacity`,
      `TestUnreadRigIsNamedWhenNoSeatIsFree`, `TestEveryRigUnreadDecidesNothing`.

## 7. Verify on the box

- [x] 7.3 Confirm a second app instance refuses to start while the service runs
