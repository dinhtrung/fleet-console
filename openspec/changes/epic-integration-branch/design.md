# Design — an epic lands on its own branch

## Context

- Dispatch resolves one base, `origin/<default_branch>` (`internal/act/git.go:52-61`), cuts
  `feature/<n>-<slug>` from it and records the sha on the dispatch row (`internal/act/actions.go:155-196`).
- The brief never names a PR base; the seat opens a draft PR and the forge defaults the target
  (`internal/act/actions.go:105-130`).
- `forge.ChangeRequest` carries number, state, head sha, branch, title, draft, mergeable and merge state — no base
  (`internal/forge/forge.go:132-160`).
- The merge gate merges by change-request number (`internal/act/merge.go:106`), and the branch mirror only parses
  `feature/<n>-` names (`internal/act/merge.go:313-334`).
- Rollup closes an epic when every child is closed, with no branch condition (`internal/act/rollup.go:59-90`);
  sweep applies the same rule to an observed epic (`internal/act/sweep.go:30-60`).

## D1. Opt-in is a project field, with a per-epic escape

`epic_branches: true` on the project (absent = false, today's behaviour). It is a project decision because the
operator wants every epic reviewed as a unit in that repository, and a per-epic label would repeat the labelling
chore the `onhold` workaround imposed.

`epic::direct` on one epic opts that epic out: its children keep targeting the default branch. The override is a
label rather than a second config key because it is per item and temporary.

## D2. Branch name and base

`epic/<n>-<slug>` (for example `epic/476-new-project-dialog-fit`), resolved by prefix `epic/<n>-` so a title edit
never renames a live branch. It is created from the default branch's head when the **first** child is dispatched,
and each later child is cut from the epic branch's current head. `Dispatch` therefore resolves its base as:
epic branch if the item's parent is an opted-in epic, default branch otherwise.

The branch is created through the **forge API**, not a local `git push`: the service already holds forge CLI
credentials for reads and merges and has no push credentials in its environment. The local `git fetch` picks the
new ref up before the worktree is cut.

No dispatch column is added: the expected base is derivable from the item's parent and the project toggle, and
`base_sha` already records the exact commit the worktree was cut from. A derived value cannot drift from a stored
one; a stored one can.

## D3. The brief names the base, and the CR carries it

The brief gains one line, `PR base: <epic branch>`; without it the seat's `gh pr create`/`glab mr create` silently
targets the default branch. `ChangeRequest` gains `Base`, parsed from GitHub `base.ref` and GitLab `target_branch`.

## D4. The gate checks the base

A child is merged only when its change request's base equals its epic's branch. A mismatch is refused with a
reason naming both bases (`expected epic/476-…, change request targets main`) and a note on the item; the operator
can retarget the PR and the next tick merges it. This check is what makes the option safe: without it, one seat
that ignores the brief lands a child on `main` and the epic is no longer isolated.

The epic's own change request is never merged by the gate, whatever its labels: its head branch is an epic branch,
and the gate only merges branches carrying an item's `Closes #<n>` **and** a dispatch. An explicit guard rejects an
epic-branch head with a clear skip so a future change cannot accidentally relax this.

## D5. Completion: one PR, opened once, merged by the operator

When every child of an opted-in epic is closed and the epic branch has commits the default branch does not, the
app opens one change request `epic/<n>-…` → default, not draft, titled with the epic, body listing every child and
carrying `Closes #<epic>`. It opens at most one: an existing open epic CR is reused, never duplicated.

Until that CR is merged, Rollup and Sweep must **not** close the epic: they skip with `integration branch open`
(naming the branch and CR). After the merge the epic closes through `Closes #<epic>` and the mirror; the skip then
no longer applies.

If a child was closed without merging, the epic PR body names it under `Not merged` so the operator sees what the
review is missing rather than reading a child list that implies it is included.

## D6. Missing branch is an error, not a recreation

If the epic branch cannot be created or read while children exist, dispatch stops with an error naming the branch
and the forge response. Recreating it from the current default head would silently drop the merged children's work
and re-open the question of what the epic contains; recovery is the operator's, after inspecting the branch.

## D7. Drift is reported, never forced

Before opening the epic CR the app asks the forge to update the branch from the default branch (GitHub's
update-branch, GitLab's rebase). A conflict is reported as `epic branch is behind and cannot be updated` and the
CR is still opened (so the operator can see the diff) but never merged by the app. The app does not force-push.

## D8. Forge parity

GitHub: `POST /git/refs` for the branch, `base.ref` on the PR, `PUT /pulls/{n}/update-branch` for the update.
GitLab: `POST /repository/branches`, `target_branch` on the MR, `PUT /merge_requests/{n}/rebase`. Both adapters
implement the same interface additions; the `fake` adapter implements them for tests. The planner's selection
rules do not change — an epic stays `epic`, a child stays `task`.

## Risks

- **A seat ignores the brief and targets main.** Mitigated by D4: the gate refuses rather than merges, and the
  refusal names both bases.
- **Long-lived branches drift.** Mitigated by D7 and by the project-level `epic_serial` default (one child at a
  time), which keeps the branch close to the review order.
- **The epic branch is deleted by hand while children remain.** D6 stops dispatch and reports it; no silent loss.
- **The reviewer treats the epic PR as a child PR.** The gate never merges a non-item branch, and the PR body says
  it is the epic's delivery, not an item's.
- **CI cost.** Each child still runs its gates against the epic branch, and the epic PR runs the full gate once
  before the operator merges; that is the price of reviewing the epic as a unit.

## Decisions ledger

| Decision | Requirement |
|---|---|
| D1 project toggle + `epic::direct` override | An epic can land on its own branch |
| D2 branch name/base, forge-created | An epic can land on its own branch |
| D3 brief + CR base | The change request carries its base |
| D4 gate refuses a wrong base | The change request carries its base |
| D5 one epic PR, operator merges, rollup held | The epic is delivered as one change request |
| D6 missing branch stops work | A missing epic branch stops the work |
| D7 drift reported | The epic is delivered as one change request |
| D8 forge parity | Every requirement, both forges |
