# Proposal — an epic lands on its own branch

## Why

Today every item is dispatched onto its own `feature/<n>-<slug>` branch cut from the default branch
(`internal/act/actions.go:158-168`, `internal/act/git.go:52-61`), and every change request targets the default
branch by omission: the brief tells the seat to open one draft PR with `Closes #<n>` and never names a base
(`internal/act/actions.go:105-130`), so the forge applies its default. Nothing in the app knows a change
request's target — `forge.ChangeRequest` has no base field (`internal/forge/forge.go:132-147`).

The consequence is that an epic's children land one by one on `main` as their reviews pass. There is no point at
which the epic exists as a reviewable unit: an operator who wants to read the whole epic before it ships has to
review every child as it merges, and a half-finished epic is indistinguishable on `main` from a finished one.
Rollup compounds it: the epic closes as soon as every child is closed (`internal/act/rollup.go:59-90`), whether or
not the work is anywhere an operator can see it together.

## What this changes

- **A project can opt in** with `epic_branches: true`. Its epics then integrate on `epic/<n>-<slug>`, cut from
  the default branch when the first child is dispatched; later children are cut from the epic branch's head.
- **Children target the epic branch**, not `main`: the brief names the base, and the mirror records a change
  request's base so the gate can check it.
- **The gate refuses a child whose base is not its epic's branch** and never merges the epic's own change request.
- **The epic is delivered as one change request** `epic/<n>-…` → default, opened by the app when the last child
  closes, with the child list in the body and `Closes #<epic>`. The app stops there; the operator reviews and
  merges it. Auto-close of the epic is held until that merge.
- **A branch that cannot be created or read stops the dispatch** — never a silent fallback to the default branch,
  because that is exactly the "children land on main" behaviour this option exists to prevent.

## Alternatives considered

| Option | Verdict |
|---|---|
| Per-epic opt-in label (e.g. `epic::branch`) | Rejected as the default: it repeats the labelling chore the `onhold` workaround already imposes. Kept as an **override**: `epic::direct` on one epic opts that epic out of the project toggle. |
| Stacked child branches (each child based on the previous) | Rejected: review and rebase a chain of N branches; one failed child blocks every later one, and there is no single merge for the epic. |
| One release-train branch for the whole project (`next`) | Rejected: loses the per-epic review boundary the operator asked for; epics become entangled. |
| App opens the final PR only as a notification/command | Rejected as the default: the operator already has a forge session; a ready PR with the child list is the review surface, and the app can open it with the same forge credentials it already uses for merges. |

## Scope

`internal/config` (one project field), `internal/act` (dispatch base, merge base check, rollup/sweep hold, epic PR
creation, rebase), `internal/forge` (read a change request's base; create a branch; create a change request),
`internal/store` only if a dispatch needs to remember the base branch (see design D2 — it does not). The board's
project form gains the toggle. No change to the planner's selection rules, the verdict contract, or the rig.

## Non-goals

- Not auto-merging the epic: the operator merges it, always.
- Not changing how standalone (non-epic) items are dispatched; they keep targeting the default branch.
- Not a release train, environment promotion, or versioning.
- Not resolving conflicts automatically: the app reports a behind/conflicted epic branch and the operator decides.
