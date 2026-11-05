# Tasks — housekeeping

## 0. Ground rules

- Gate: `go build ./... && go vet ./... && go test ./...` and `openspec validate --all --strict`. Real output.
- Fixed vocabulary: package `internal/housekeeping`; commands `--housekeep`, `--housekeep --apply`; config
  `housekeeping_interval`; audit kinds `housekeeping.remove`, `housekeeping.summary`; skip reasons
  `open-cr`, `live-dispatch`, `live-row`, `dirty`, `symlink`, `outside-root`, `forge-stale`.
- `internal/act/tick.go` and `internal/config/config.go` are shared with #157: rebase before opening the PR.
- Never `rm -rf`, never `--force`, never a process kill, never a journal vacuum.

## 1. Selection and execution (builder 1)

- [x] 1.1 `internal/housekeeping`: `Candidate{Item int; Path, Branch string; Dirty bool; HasOpenCR, LiveDispatch,
      LiveRow bool}` and `Select(candidates) []Decision` — pure, with a reason per kept candidate.
      Evidence: a table test with one case per keep condition and one removable case.
- [x] 1.2 `Execute(ctx, decisions, git)` — scoped to the root, `wt-<key>-<n>` only, symlink skip, `git worktree
      remove` without `--force`, then `git worktree prune`. A git refusal is a keep with git's reason.
      Evidence: a fake git runner recording argv; a symlink case that records no removal; a dirty case that records
      no removal.
- [x] 1.3 Collect the four facts from the tick's own read (open CRs, dispatch rows, queue rows) and
      `git status --porcelain`; a stale forge read returns "do nothing" with `forge-stale`.
      Evidence: tick-level test — one open CR keeps its worktree, one closed item's clean worktree is removed, a
      stale read removes nothing.

## 2. Surface, schedule and audit (builder 1, after 1)

- [ ] 2.1 `--housekeep [--apply]`: the plan, the summary, exit 0 even when removals fail (the failure is in the
      events), and `--apply` gated by the same four conditions.
- [ ] 2.2 Tick schedule: `housekeeping_interval` (default 0), last-run stamp in memory, run **after** the dispatch
      phase, never at boot.
- [ ] 2.3 Audit: one event per removal, one summary per run with the counts; a failed removal carries the error and
      is not counted as removed.
      Evidence: the event rows from a test run, quoted.
- [ ] 2.4 `docs/my-stack.md`: what it will and will not touch, why the operator script still exists for process and
      journal reclamation, and the dry-run command.
