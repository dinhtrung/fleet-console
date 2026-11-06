# Proposal — reclaim the box from the app, safely

## Why

Two failures are already in this box's record: a tick at nine open pull requests drove load to 30 and sshd stopped
answering banners, and a set of agent seats exhausted swap with per-seat language servers. Both were recovered by an
operator running a shell script that lives **outside the repository** — the recovery recipe is a Hermes-skill
artifact (`scripts/housekeeping.sh`), not part of the product.

What it does is exactly what a control plane should own, because the app is the only component that knows which
worktrees are still spoken for: it removes worktrees whose branch has no open change request, and it prunes. And
what it does that this app must not: it kills processes it did not start and vacuums the user journal.

The result today is that disk and memory reclamation is a cron of an untracked script, unlogged, unaudited, and
unavailable on a box where nobody installed it.

## What changes

- `internal/housekeeping`: a pure selection over worktrees (`keep` / `remove`) plus an executor that runs
  `git worktree remove` (never `--force`) and `git worktree prune`.
- A worktree is kept when **any** of these holds: its branch is the head of an open change request, the item has a
  live dispatch row, the item has a non-terminal queue row, or the working tree is dirty. Everything else under the
  project's worktree root is removed.
- Runs on `housekeeping_interval` (default off), and by hand with `--housekeep [--apply]`. Dry run is the default;
  each removal is one audit event; one summary line per run.

## Losses, named

- **No process reaping and no journal vacuum.** The app kills what it started and nothing else; a language server
  another tool launched is that tool's business. The operator script stays the escape hatch for a wedged box, and
  the docs say so.
- **A dirty worktree is never reclaimed**, even when its item is closed. That is deliberate: uncommitted work is the
  one thing a GC cannot reconstruct. The summary counts them so a box filling with dirty worktrees is visible.
- **A stale forge read blocks removal.** With the change-request read failed, every branch looks like "no open CR";
  the run refuses instead.

## Capabilities

### New Capabilities

- `housekeeping`: the keep/remove rule, the scope and safety of removal, the dry-run default, the audit trail and
  the interval.

## Impact

- `internal/housekeeping` (new) — selection and execution, both testable with a fake git runner.
- `internal/act/tick.go` — one call per tick when the interval allows, after the dispatch phase.
- `cmd/fleet-console/main.go` — `--housekeep [--apply]` for the operator.
- `internal/config` — `housekeeping_interval`.
- `docs/my-stack.md` — what it will and will not touch.
