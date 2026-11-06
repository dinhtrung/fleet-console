## 0. Ground rules

- Gate: `make check` (or `go build ./... && go vet ./... && go test ./...`); paste the real output.
- Read `openspec/changes/merge-adopted-crs/{proposal.md,design.md}` first, then `specs/act-merge/spec.md`. D2 is the
  decision that must not be re-litigated: **no synthetic dispatch row.**
- Do not weaken an existing refusal to make adoption pass. If a guard fires on a real pull request, that is the guard
  working; report it.

## 1. Verdict capture does not depend on a dispatch row (builder 1)

- [x] 1.1 Make `CaptureFromForge` (and the store write behind it) work for an item with no dispatch: the verdict key
      is the change request number plus the sha, the dispatch id becomes optional/zero. Keep the existing dispatched
      behaviour byte-identical for rows that have one.
- [x] 1.2 In `internal/act/tick.go`, move the capture **above** the `latest[item]` lookup so an item with no dispatch
      still gets its verdict stored, and the skip line for a refused merge reports the real missing fact.
- [x] 1.3 Tests: verdict at the head sha with a dispatch → stored as today; verdict with no dispatch → stored, and a
      later `Merge`/`MergeAdopted` finds it; no verdict comment → nothing stored and the skip names "no ACCEPT verdict".

## 2. The adoption path (builder 1, same branch)

- [x] 2.1 `internal/act/merge.go`: add the dispatch-free arm. `MergeAdopted(ctx, item int, request forge.ChangeRequest)`
      enforces the same four facts plus the two refusals (live dispatch on a different branch; branch not
      `feature/<item>-`), writes `act/change_request.adopted` **before** `Forge.Merge`, then the `merge` event, then
      `Finalize` with no dispatch. It must not call `EndDispatch`.
- [x] 2.2 `Finalize` with no dispatch: verify closed → close when the body only referenced → clear the marker → note
      the next unblocked item. No `dispatch.*` events for an adopted item.
- [x] 2.3 `internal/act/tick.go`: when `latest[item]` is absent, attempt adoption instead of the bare `continue`; a
      refusal keeps the `#<item>: change request #<n> ...` skip shape and names the reason.
- [x] 2.4 Tests (fake forge, the package's existing style): adoption merges and closes; missing verdict refuses;
      stale verdict at another sha refuses; branch prefix wrong refuses naming the branch; second branch on a live
      dispatch refuses naming both; and the dispatched path's existing tests still pass unchanged.
- [x] 2.5 Evidence: the test names, and for each refusal the exact skip string produced.

## 3. End-to-end regression + docs (builder 2, blocked by 1 and 2)

- [x] 3.1 One end-to-end test that reproduces the reported failure: an item whose row was created outside the app (no
      dispatch row), a pull request with `Closes #<item>`, green checks, `ACCEPT <head>`; assert merge happened, item
      closed, both events written, and **no** `dispatch` row exists afterwards.
- [x] 3.2 Prove the old behaviour is gone: on the same fixture, the first tick's report contains no
      `closes no dispatch` line (grep the rendered skip list).
- [x] 3.3 Docs: one paragraph each in the watchdog section (`openspec/changes/fleet-console-v1/design.md` §11 and
      `docs/fleet-console.md` § The merge watchdog) and `PRODUCT.md` (a new "What the app merges") stating adoption,
      its guards and that readiness stays the integrator's job. (`DESIGN.md` was named in the original brief, but it
      is the inherited visual design system — colors, typography, components — and documents no merge behaviour;
      `openspec/project.md` keeps behaviour out of it. The watchdog is specified in the two files above.)
- [x] 3.4 Tick tasks 3.1–3.3 in `openspec/changes/merge-adopted-crs/tasks.md` and paste the validate output.

## 4. Orchestrator

- [ ] 4.1 After merge: re-run the reported case on the box — dispatch a throwaway item by hand, ACCEPT it, and quote
      the journal lines showing `change_request.adopted` then `merge` — then close the epic.
