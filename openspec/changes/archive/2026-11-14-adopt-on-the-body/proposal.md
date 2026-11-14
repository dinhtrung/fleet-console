# Proposal — adoption reads the body, not the branch

## Why

Three change requests carried a reviewer's `ACCEPT` at their current head and still did not merge. Two of them are
refused by one line:

```
act-skip: fleet-console: #450 merge waiting: refusing merge: change request #444 is on
fix/delete-project-verdicts-by-project-id, which does not start with feature/450-
act-skip: fleet-console: #451 merge waiting: refusing merge: change request #449 is on
fix/438-follow-empty-rigs-and-recovered-read, which does not start with feature/451-
```

`MergeAdopted` (`internal/act/merge.go:315,346`) requires the branch to start `feature/<item>-`. The rule came from
#314, where an item was inferred from a title; its purpose is to stop the gate **guessing** which item a change request
belongs to. But by the time this check runs the app has already read `Closes #450` from the body — an explicit,
machine-checked statement. The branch name adds nothing except a spelling requirement the rig does not follow: it names
branches `fix/<slug>` and `fix/<issue>-<slug>`, both of which are ordinary.

The result is worse than a refused merge: a reviewed, green change request sits forever, the dispatch that produced it
is over, and the item looks stuck with no reason a human can act on.

## What this changes

- **`Closes #n` in the body adopts the item.** The branch is a consistency check, not the adopter.
- **A branch naming a different item is refused**, naming both numbers: that is evidence of mis-homing and a human
  decides, not the gate.
- **A branch naming no item merges with one warning** per head, naming the convention — a log line, not a refusal.
- **A branch that names the item behaves exactly as today**, with no warning.

## Non-goals

- Not weakening the live-dispatch refusal (`merge.go:335-343`): two branches closing one item is still a human call.
- Not dropping `Closes #n`. The body check stays the adoption signal; nothing is inferred from a title.
- Not touching `Merge` (the dispatched path), which knows its item from the dispatch row.
