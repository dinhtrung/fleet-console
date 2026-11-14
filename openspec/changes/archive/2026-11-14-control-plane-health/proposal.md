# Proposal — the control plane can say whether it is working

## Why

An audit of the shipped capabilities against `PRODUCT.md`'s promise ("mirrors the forge and the rig, decides what is
ready, dispatches, collects verdicts, merges on four facts, keeps every human obligation as a record with an SLA")
found the decision and merge halves solid and the **truth-telling** half missing. Four measurements from this box:

**1. Health is a literal.** `internal/web/app.go:89` answers `/api/health` with
`{"status":"ok"}` — no database, no queue, no forge, no tick. The exact failure that cost an operator a service
restart today (a loop idling on its backoff, indistinguishable from a dead daemon) reports **ok**.

**2. The mirror keeps a heartbeat nobody reads.** `store.ReadMirrorMeta` (`internal/store/mirror.go:146`) returns
`last_forge_at` and `last_rig_at` per project, written on every successful read. No web file references it: the board
cannot say "last forge read 12 minutes ago", so a stalled mirror and a quiet forge look the same.

**3. The ledger grows without a floor or a ceiling.** On homebox: `event` 2,419 rows since 2026-10-30, `dispatch` 286,
`verdict` 104. Nothing prunes, exports or archives: `grep -rn "DELETE FROM event" internal/` finds no writer, and the
housekeeping pass only selects worktrees (`internal/housekeeping/collect.go`).

**4. A human obligation has a nudge but no clock the operator can see.** `decide.RenudgeAfter` (15 m, then one per
cooldown) nudges a *seat* about a row. The board shows no age and no due time for a row raised to `human@kernel`, so
"SLA" is a word in the product doc, not a number anywhere a human can act on.

The audit also re-found a gap a rig issue already named: the reviewer seat is **hard-coded** as
`dev-review@<rig>` (`internal/act/review.go:53,130`). The rig filed #384 for it and closed it **without a merge** —
`git show origin/main:internal/act/review.go` still carries both literals — so a project whose rig names its
reviewer anything else (`dev.review`, `dev-reviewer`) gets review rows nobody owns.

## The inventory, in one line each

Shipped and covered by specs: project registry and its file write, mirror of forge and rig, dispatch by a written
contract, verdict capture (anchored grammar + text scan), four-fact merge, merge repair, epic rollup and sweep,
decision notes and replies, progress notices, forge webhook trigger, housekeeping of worktrees, rig tiers, settings,
seat transcripts, verdict relay and operator questions.

Missing or weak, ranked:

| # | Gap | Evidence | Priority |
|---|-----|----------|----------|
| G1 | Health/readiness is a constant | `app.go:89` | P1 |
| G2 | No staleness of forge/rig reads on any surface | `mirror_meta` has no reader in `internal/web` | P1 |
| G3 | Ledger retention and export absent | no prune path; 2,419 events | P2 |
| G4 | Reviewer seat hard-coded | `act/review.go:53,130`; #384 closed unmerged | P1 |
| G5 | Operator rows have no visible age/deadline | `plan.go:30` nudges seats only | P2 |
| G6 | Local branch hygiene (worktrees only today) | `housekeeping/collect.go` | P3 |

## Scope of this change set

G1, G2, G3. They are one story — *the board must be able to tell the truth about itself* — and they are the ones an
operator needs before trusting anything else the board says. G4 is a three-line change with an existing issue and
belongs in its own PR (the rig closed #384 without it; re-file or reopen rather than bury it here). G5 needs the owner
to pick numbers (deadline per purpose) before it can be specified. G6 is a cleanup feature with no correctness risk.

## Impact

`internal/web` (health handler, project header, one read), `internal/store` (a bounded delete + a count query),
`internal/act/housekeep.go` (the retention step), `openspec/specs` (`health` new, `housekeeping` and
`board-navigation` amended). No schema change beyond an index if the delete needs one.
