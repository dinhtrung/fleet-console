# Design — adoption reads the body, not the branch

## D1. The body adopts; the branch only agrees or disagrees

`MergeAdopted` already requires `Closes #<item>` in the body and already reads the item from it. The branch check that
follows adds no information — it only asserts a spelling. Deleting it outright would also delete a useful signal, so it
becomes a **comparison**: if the branch carries an item number, it must be the same number; if it carries none, the
merge proceeds with a warning.

Parsing: take the last path segment before the first `-`, and accept it only when it is all digits. `feature/450-x`
gives 450; `fix/delete-project-verdicts-by-project-id` gives none; `fix/438-follow-empty-rigs` gives 438.

`Rejected:` requiring a fixed set of prefixes (`feature/`, `fix/`, `docs/`). The prefix carries no meaning to the gate,
and enumerating it makes the next convention a bug report.

## D2. A mismatch is a refusal, and it names both numbers

`fix/438-…` closing `#451` is not a naming quirk: the branch was written for another item, and the gate cannot know
whether the work was re-homed deliberately. The refusal reads
`change request #449 is on fix/438-… (#438) but closes #451` so the operator can re-home the branch or open the right
item.

## D3. A missing number is one warning per head

The warning is deduped the way every other raise is (`item + head`), so a change request that waits for review for an
hour produces one line. It names the convention and the exact rename a human would make:
`#444 is on fix/delete-project-verdicts-by-project-id, which names no item; the convention is <prefix>/<item>-<slug>`.

## D4. What still refuses

- A `Closes #n` that names a closed item.
- A `Closes #n` that names an item with a live dispatch on another branch (unchanged, `merge.go:335-343`).
- No `ACCEPT` at the head.
- Checks not green, or the request not mergeable.

Those four are facts about the work. The branch name was never one of them.
