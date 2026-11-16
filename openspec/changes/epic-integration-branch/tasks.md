# Tasks — an epic lands on its own branch

## 1. Contract

- [x] 1.1 Author the change set and validate it. Evidence: `openspec validate epic-integration-branch --strict`
  and `openspec validate --all --strict`.
- [ ] 1.2 Record the two operator-facing strings in the design before coding: the refusal reason
  (`wrong_base: change request targets <got>, expected <want>`) and the rollup hold (`integration branch open`).
  Evidence: both strings appear in `design.md` and in a test name.

## 2. Config and surface

- [x] 2.1 Add `EpicBranches bool \`yaml:"epic_branches"\`` to `config.Project`
  (`internal/config/config.go:40-90`), validate nothing new, thread it through `store.UpsertProject` and the
  project read (`internal/store/upsert.go`, `internal/store/mirror.go`) in the same shape as `epic_serial`.
  Evidence: a config with `epic_branches: true` round-trips through `--apply-config` and `GET /api/projects`.
  **Done (PR #492 + #487):** #492 landed the field (as `*bool`, so an absent key leaves the row alone) and proved
  the `--apply-config` half against SQLite; #487 wired the same value through the board's API and proved the read
  half — `TestProjectEpicBranchesRoundTrip` shows `"epic_branches":true` on `GET /api/projects` after a create.
- [x] 2.2 Add the checkbox to the project form next to `epic_serial` (`src/screens/ProjectsScreen.tsx`) and to the
  edit draft, defaulting off. Evidence: screenshot of the create dialog with the field, and a create that persists
  it.
  **Done (PR #487):** the "Land each epic on its own branch" checkbox sits beside the epic_serial one, defaults off,
  and `draftFromProject` carries it back into the edit dialog; the create-dialog screenshot is in the PR body, and
  `TestProjectEpicBranchesRoundTrip` proves a create persists `true` (file and row) and an edit clears it.

## 3. Forge

- [x] 3.1 Add `Base string` to `forge.ChangeRequest` (`internal/forge/forge.go:132-160`), parse GitHub `base.ref`
  and GitLab `target_branch`, and update the fixtures. Evidence: `go test ./internal/forge -run TestGitHubChangeRequests`,
  the GitLab counterpart, and one fixture line showing `"base": {"ref": "epic/476-…"}` / `"target_branch"`.
  **Done (PR #493):** `ChangeRequest.Base` is read from `base.ref` / `target_branch`; the GitHub and GitLab fixtures
  carry `epic/476-new-project-dialog-fit`, and `TestGitHubChangeRequests` and
  `TestGitLabChangeRequestsSkipsDetailsForMerged` assert `Base` equals it.
- [x] 3.2 Add the three writes to the `Forge` interface and both adapters plus `fake`: create a branch from a ref,
  open a change request with an explicit base, update a branch from its base. Evidence:
  `go test ./internal/forge` with the fake asserting each argv (`gh api --method POST …/git/refs`,
  `gh pr create --base`, `glab api --method POST …/repository/branches`, `glab mr create --target-branch`,
  `gh api --method PUT …/update-branch`, `glab api --method PUT …/rebase`).
  **Done (PR #493):** `EnsureBranch`, `OpenChangeRequest` and `UpdateBranch` are on the interface, both adapters and
  the fake; `TestEpicBranchWriteArgv` records and asserts all six argv (pasted in the PR body), and
  `TestEnsureBranchTreatsExistingAsSuccess` covers the existing-branch and server-error arms. The interface guard
  walks all 15 methods.

## 4. Dispatch

- [x] 4.1 Resolve the base per item: parent epic opted in and not `epic::direct` → `epic/<n>-<slug>`; otherwise the
  default branch. Create the epic branch when absent, then cut the task's worktree from its head
  (`internal/act/actions.go:155-196`, `internal/act/git.go:52-61`). Evidence: a test where the first dispatch
  creates the branch and the second base-sha equals the epic head.
  **Done (PR #495):** `epicBranchFor` applies the three-fact rule; `Dispatch` calls `EnsureBranch` from the default
  head, re-fetches and resolves the epic head through the new `Git.RefSHA`. `TestDispatchEpicChildCutsFromEpicHead`
  proves the first child cuts from the default head and the second from the epic head; `TestDispatchEpicDirect…` and
  `TestDispatchEpicBranchesOff…` keep the default base.
- [x] 4.2 Add `PR base: <branch>` to the brief (`internal/act/actions.go:105-130`) and assert it in
  `brief_publish_test.go`. Evidence: the rendered brief in the test output.
  **Done (PR #495):** an epic child's brief opens `## Publish` with `PR base: epic/<n>-<slug>`; a non-epic brief is
  byte-identical (the placeholder renders empty). The rendered brief for both cases is logged in the test output
  and pasted in the PR body; `TestBriefPublishContract` still passes unchanged.
- [x] 4.3 Refuse the dispatch when the branch cannot be created or read; no fallback to the default branch and no
  recreation over existing work. Evidence: two tests, one per design D6 arm, quoting the error text.
  **Done (PR #495):** `TestDispatchRefusesWhenEpicBranchCannotBeCreated` and
  `TestDispatchRefusesWhenEpicBranchCannotBeRead` quote the errors; both also assert no dispatch row and no worktree
  were written, so there is no fallback or recreation.

## 5. Merge gate

- [x] 5.1 Refuse a task change request whose `Base` is not its epic branch, with the reason string from 1.2 and a
  note on the item (`internal/act/merge.go:106`, `internal/act/review.go`). Evidence: a test where the CR targets
  `main` and the merge does not happen; the note text is asserted.
  **Done (PR #496):** `checkBase` runs on both merge paths before any mutation; the reason is
  `wrong_base: change request targets <got>, expected <want>` (the epic branch matched by prefix `epic/<n>-`), and
  `noteWrongBase` leaves one deduped item comment telling the operator to retarget.
  `TestMergeRefusesAChildWhoseBaseIsNotItsEpicBranch` asserts the reason, the note and that no merge happened. The
  base check runs only for an opted-in project, so a toggle-off project merges exactly as before.
- [x] 5.2 Guard the epic's own change request: a head branch matching `epic/<n>-` is never merged by the gate.
  Evidence: a test with an accepted epic CR that stays unmerged and a skip line naming the branch.
  **Done (PR #496):** `refuseEpicHead` runs first on both `Merge` and `MergeAdopted`, so the dispatch path and the
  no-dispatch adoption path both refuse with
  `refusing merge: change request #N is the epic delivery on <branch> (epic #n); the gate never merges an epic, the operator does`.
  `TestGateNeverMergesTheEpicDelivery` covers both paths and asserts neither merged.

## 6. Completion and rollup

- [x] 6.1 Extend the branch mirror to parse `epic/<n>-` alongside `feature/<n>-` (`internal/act/merge.go:313-334`),
  so the tick can see the epic branch's open/merged change request. Evidence: a branch-parsing table test with both
  prefixes.
  **Done (PR #497):** `branchItem` reads `feature/<n>-` and `epic/<n>-` alike; `epicBranchNumber` is the epic-only
  view and `openEpicDelivery` finds the epic's open request (never a merged/closed one, never a feature branch).
  `TestBranchParsingReadsBothPrefixes` is the table test; `TestOpenEpicDeliveryFindsTheDelivery` covers the filter.
- [x] 6.2 When every child is closed and the epic branch is ahead, open exactly one epic request with the child
  list, `Not merged` section and `Closes #<epic>`; reuse an open one. Evidence: a test asserting one call, the body
  lines, and zero calls on the second tick.
  **Done (PR #497):** `completeEpic` opens one non-draft `epic/<n>-` → default request titled `Epic #n: <title>`,
  the body listing every child under `Merged`/`Not merged` and carrying `Closes #<epic>`. Both the sweep and the
  rollup path reach it; a `completed` map keeps one per tick and the open delivery holds later ticks.
  `TestCompletionOpensOneEpicRequest` asserts one call, the body lines and zero on the second tick;
  `TestRollupPathCompletesOnLastChild` covers the merge path.
- [x] 6.3 Hold Rollup and Sweep while the epic request is open: skip with `integration branch open`
  (`internal/act/rollup.go:59-90`, `internal/act/sweep.go:30-60`). Evidence: tests for both functions and the tick
  line.
  **Done (PR #497):** `Rollup` and `Sweep` take the delivery and return
  `integration branch open: <branch> has change request #<n>` — not quiet, so the tick's line names both.
  `TestRollupAndSweepHoldOnAnOpenDelivery` covers both; the second-tick assertion in
  `TestCompletionOpensOneEpicRequest` checks the tick's skip line.
- [x] 6.4 Ask the forge to update the epic branch before opening the request; report a conflict and still open the
  request. Evidence: a fake returning a conflict and the test's skip/notice line.
  **Done (PR #497):** after opening, when the default has commits the epic branch lacks, `completeEpic` asks
  `Forge.UpdateBranch`; a refusal records `epic branch is behind and cannot be updated` and the request stays open.
  The forge's update is per change request, so it runs with the request as its handle — the outcome (reported, never
  forced) is D7's. `TestCompletionConflictStillOpens` quotes the notice.

## 7. Docs and gates

- [ ] 7.1 Document the option and the operator's job in `docs/` (what the toggle does, what the epic PR contains,
  what a wrong-base refusal means, what to do when the branch conflicts). Evidence: the page and a link from the
  project form's hint.
- [ ] 7.2 Run `go test ./...`, `go vet ./...`, `npm run typecheck`, `npm run lint`, `npm run build`,
  `openspec validate --all --strict`; paste real output in the PR. Evidence: the commands and exit codes.
