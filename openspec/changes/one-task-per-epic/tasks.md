# Tasks — one task per epic in flight

## 1. The rule

- [ ] 1.1 `internal/decide`: add `ReasonEpicBusy Reason = "epic_busy"`.
- [ ] 1.2 Build the holder set from the snapshot: for each open item with a parent, it holds the epic when it has a
      live dispatch row or its branch carries an open unmerged change request. Put this next to the existing parent
      checks in `decide.go`.
- [ ] 1.3 In the ready loop, skip a child whose parent is held by another child, with detail `#<n> in flight` or
      `#<n> in review`.
- [ ] 1.4 Take siblings in ascending number order, so a tick's choice is stable (the ready loop already walks the items
      in number order — assert it rather than assume it).
- [ ] 1.5 A sibling on `onhold` holds nothing: it is skipped by the existing rule and must not enter the holder set.

## 2. The setting

- [ ] 2.1 `internal/config`: `epic_serial` on a project, default `true`; validated like the other booleans; shown in
      `--print-config`.
- [ ] 2.2 `EpicSerialFor(key)` on the effective config, used by the caller that builds the decide input.
- [ ] 2.3 Test: unset → true; explicit false → siblings dispatch in parallel.

## 3. Proof

- [ ] 3.1 `TestEpicSerialOneChildInFlight`: two children, two free seats → one dispatch, one `epic_busy` naming it.
- [ ] 3.2 `TestEpicSerialAdvancesOnMerge`: holder's CR merged → next tick dispatches the next sibling in number order.
- [ ] 3.3 `TestEpicSerialHoldsOnOpenCR`: CR open, dispatch ended → still skipped, detail says in review.
- [ ] 3.4 `TestEpicSerialIgnoresOnHoldSibling`: a sibling carrying `onhold` does not block.
- [ ] 3.5 `TestEpicSerialOffDispatchesBoth`: project sets `epic_serial: false` → both children dispatch.
- [ ] 3.6 `TestEpicSerialLeavesUnparentedItemsAlone`: two parentless items still dispatch together.
- [ ] 3.7 `TestEpicSerialHoldsOnSilentSeat`: a live dispatch whose seat is silent for hours keeps the epic held — one
      dispatch in the plan, no second builder, and the silent-dispatch question is the one that speaks.
- [ ] 3.8 `TestEpicSerialHoldsOnDraftCR`: an open unmerged CR with no live dispatch still holds, whether or not it is a
      draft (the snapshot does not distinguish).
- [ ] 3.9 `TestEpicSerialGrandfathersTwoInFlight`: two children already dispatched → no third child while both are live.

## 4. Docs and gate

- [ ] 4.1 `docs/fleet-console.md`: one short section — why an epic advances one child at a time, what `epic_busy` means,
      and when to set `epic_serial: false`.
- [ ] 4.2 `go build ./... && go vet ./... && go test ./...` with output pasted in the PR.
- [ ] 4.3 `openspec validate --all --strict`.
