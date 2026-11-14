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

- [x] 3.1 `TestEpicSerialOneChildInFlight`: two children, two free seats → one dispatch, one `epic_busy` naming it.
- [x] 3.2 `TestEpicSerialAdvancesOnMerge`: holder's CR merged → next tick dispatches the next sibling in number order.
- [x] 3.3 `TestEpicSerialHoldsOnOpenCR`: CR open, dispatch ended → still skipped, detail says in review.
- [x] 3.4 `TestEpicSerialIgnoresOnHoldSibling`: a sibling carrying `onhold` does not block.
- [x] 3.5 `TestEpicSerialOffDispatchesBoth`: project sets `epic_serial: false` → both children dispatch.
- [x] 3.6 `TestEpicSerialLeavesUnparentedItemsAlone`: two parentless items still dispatch together.
- [x] 3.7 `TestEpicSerialHoldsOnSilentSeat`: a live dispatch whose seat is silent for hours keeps the epic held — one
      dispatch in the plan, no second builder, and the silent-dispatch question is the one that speaks.
      Shipped: the row is six hours old and still live, so no child dispatches, the sibling is skipped `epic_busy`
      naming the holder `#5 in flight`, and the holder itself keeps its existing `live_row` skip. Decide reads no
      age at all — the silent-dispatch question is raised by the act side from the same row — so what is pinned here
      is that a quiet row is never treated as ended.
- [x] 3.8 `TestEpicSerialHoldsOnDraftCR`: an open unmerged CR with no live dispatch still holds, whether or not it is a
      draft (the snapshot does not distinguish).
      Shipped as a pair, because the two halves live in different packages: `decide`'s test pins the hold and the
      `#5 in review` skip from `Branch{HasOpenCR: true}`, and `internal/mirror`'s
      `TestBuildSnapshotOpenCRIncludesADraft` pins the fact that can actually regress — an open change request with
      `Draft: true` projects to `HasOpenCR`, since `decide` sees no draft flag at all.
- [x] 3.9 `TestEpicSerialGrandfathersTwoInFlight`: two children already dispatched → no third child while both are live.
      Shipped: two live rows, no dispatch at all, both holders keep `live_row` (nothing is cancelled or re-planned),
      and the third child is skipped `epic_busy`.
- [x] 3.10 `TestEpicSerialFreesOnADispatchThatEndedWithoutACR`: the spec's "A dispatch that ends without work"
      scenario, which had neither a test nor a task. The ended dispatch belongs to the higher-numbered child on
      purpose: a row still counting as a holder would block the lower child too, so "the lowest child goes out after
      the row ends" is the observable difference between freed and still-held.

## 4. Docs and gate

- [ ] 4.1 `docs/fleet-console.md`: one short section — why an epic advances one child at a time, what `epic_busy` means,
      and when to set `epic_serial: false`.
- [ ] 4.2 `go build ./... && go vet ./... && go test ./...` with output pasted in the PR.
- [ ] 4.3 `openspec validate --all --strict`.
