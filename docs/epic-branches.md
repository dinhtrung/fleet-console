# Epic branches

A project can land each epic as **one** reviewable change request on its own branch, instead of merging its children
onto the default branch one at a time. The option is off by default; a project that never turns it on behaves exactly
as before.

## Turn it on

Two equivalent paths:

- `epic_branches: true` on the project's entry in `config.yaml`.
- The board's **Projects → New project / Edit** form: **Land each epic on its own branch**.

The value is stored on the project row, like `onhold`: `--apply-config` copies a file key into the row and the board
writes the row directly. A file that omits `epic_branches` leaves the row's value alone; to change it, name it
explicitly (`epic_branches: false` turns it off).

One epic can opt out with the label **`epic::direct`**: that epic's children keep targeting the default branch. The
escape is a label because it is per item and temporary; there is no per-epic opt-in UI.

## What happens at dispatch

For the **first** child of an opted-in epic, dispatch creates `epic/<n>-<slug>` (the epic's number and its title at
that moment) from the default branch's head, through the forge API — the service holds no push credentials — and then
cuts the child's worktree from the epic branch's head. Later children of the same epic are cut from the epic branch's
head, so each child builds on the previous child's merged work.

The child's brief carries one extra line in its `## Publish` section:

```
PR base: epic/<n>-<slug>
```

so the seat's `gh pr create` / `glab mr create` targets the epic branch rather than the default. An ordinary item's
brief is unchanged and names no base.

## The epic change request

When **every** child of the epic is closed and the epic branch has commits the default branch does not, the app opens
exactly one non-draft change request `epic/<n>-<slug>` → default:

- title `Epic #n: <title>`;
- body listing every child under `### Merged` and `### Not merged`, then `Closes #<n>`.

It opens once: an existing open epic request is reused, never duplicated; a second tick with the request open opens
nothing. A child that closed without a merged change request is named under `Not merged`, so the review shows what is
missing rather than implying it is included.

**The app never merges the epic request** — not on a verdict, not on a label, not on any condition. You merge it. Until
you do, rollup and the epic sweep hold the epic open and skip with:

```
integration branch open: epic/<n>-<slug> has change request #<cr>
```

## `wrong_base`

In a project with the toggle on, the merge gate merges a child only when its change request targets the expected
base: the epic branch, matched by prefix `epic/<n>-`, for a child of an open epic, or the project's default branch for
any other item. A mismatch refuses the merge:

```
refusing merge: wrong_base: change request targets <got>, expected <want>
```

and leaves one comment on the item telling you to retarget. Retarget the change request's base branch to the expected
one (the epic branch, or `main`); the next tick merges it once the verdict still names the head. A wrong-targeted
request is never marked ready.

## When the branch is behind or conflicted

Before delivery, when the default branch has commits the epic branch lacks, the app asks the forge to update the epic
branch from the default (GitHub *update-branch*, GitLab *rebase*). If the forge refuses — a conflict — the tick records

```
epic branch is behind and cannot be updated: <reason>
```

and **still opens** the request so you can see the diff. The app never force-pushes. Resolve the conflict on the epic
branch and update it yourself; the request follows the branch.

## Edge cases you may meet

- **Retitling an epic between dispatches** changes the name a later dispatch would create, so the epic can end up with
  two `epic/<n>-` branches and earlier merged work on the first. Delivery resolves the branch by the `epic/<n>-` prefix;
  if it finds two, it refuses to guess and names both. Do not retitle an epic while its children are in flight.
- **A child whose epic closed mid-flight** falls back to the default base and is refused `wrong_base`. The gate reads
  the parent only while it is open, so close the epic after its last child.
- **A missing mirror row** for the item or its parent also falls back to the default base and refuses rather than
  guessing. That is the safe direction; it clears on the next mirror pass.
- **A delivery closed without merging** can be reopened on a later tick: nothing records that it was once opened.
- **The hold is not gated by the toggle.** An open `epic/<n>-` change request holds rollup and the sweep even if the
  project turns the option off afterwards. Handle or close that request.

## What this does not do

- No release train, environment promotion or versioning: an epic branch is a review unit, not a version.
- No auto-merge of the epic request, and no merge on a review verdict.
- No conflict resolution and no forced updates.
- No change for standalone items, or for any project without the toggle: they keep targeting the default branch, and
  the base check does not run.
