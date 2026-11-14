# Tasks — adoption reads the body, not the branch

## 1. The rule

- [x] 1.1 `internal/act/merge.go`: replace the `branchPrefix` prefix test in `MergeAdopted` with a comparison of the
      item number the branch carries (last segment up to the first `-`, digits only) against the item from the body.
- [x] 1.2 Branch carries the same number → proceed as today. Different number naming an **open** item → refuse, naming
      both numbers. Different number naming a **closed** item → merge, one warning saying it is re-homed. No number →
      merge, one warning naming the convention.
- [x] 1.3 Keep the live-dispatch-on-another-branch refusal and the `Closes #n` requirement untouched.
- [x] 1.4 Delete `branchPrefix` if it has no other caller; if it does, leave it and say so in the PR body.

## 2. The warning

- [x] 2.1 Dedupe the warning on `(item, head)` through the same ledger every other raise uses, one line per pair.
- [x] 2.2 Test: three ticks, one head → one warning; a new head → a second warning.

## 3. Proof

- [x] 3.1 `TestAdoptedMergeAcceptsABranchWithNoItem`: `fix/<slug>` + `Closes #450` → merged, one warning.
- [x] 3.2 `TestAdoptedMergeAcceptsFixPrefixedBranch`: `fix/450-…` → merged, no warning.
- [x] 3.3 `TestAdoptedMergeRefusesABranchNamingAnotherOpenItem`: `fix/438-…`, #438 open, `Closes #451` → refused.
- [x] 3.3a `TestAdoptedMergeAcceptsABranchNamingAClosedItem`: `fix/438-…`, #438 closed, `Closes #451` → merged + warning.
- [x] 3.4 `TestAdoptedMergeFeatureBranchUnchanged`: `feature/450-…` → merged, no warning (today's behaviour).
- [x] 3.5 `TestAdoptedMergeStillRefusesLiveDispatchOnAnotherBranch`: unchanged refusal still fires.

## 4. Docs and gate

- [x] 4.1 `docs/fleet-console.md`: the branch convention is a convention, not a gate; what a mismatch means.
- [x] 4.2 `go build ./... && go vet ./... && go test ./...` with output pasted in the PR.
- [x] 4.3 `openspec validate --all --strict`.
