# Tasks — the control plane can say whether it is working

## 1. Readiness

- [x] 1.1 `internal/web/health.go`: `/api/ready` collects four facts — store read, per-project mirror heartbeat
      against `2 × tick_interval`, the rig queue's last error and its age (cached 30 s), the last completed tick per
      project — and answers 200 with an empty failure list or 503 with the failures named and their numbers.
- [x] 1.2 `/api/health` keeps its constant body, with a comment saying why it must not read a dependency.
- [x] 1.3 Tests: healthy 200; a stale heartbeat 503 naming `mirror.<project>`; a slow read inside the threshold still
      200; dependencies down still 200 on `/api/health`.

## 2. Staleness on the board

- [x] 2.1 `mirror_meta` reaches the project surface: `last_forge_at`, `last_rig_at`, their ages, and the two
      thresholds (`2 ×` quiet, `5 ×` stalled) computed once, server-side, and raised to the poll's own
      `idle_poll_max` window when the backoff makes it longer (#408).
- [x] 2.2 A project that has never read says so in words.
- [x] 2.3 Tests: age formatting, both thresholds, and the never-read case.

## 3. Retention

- [x] 3.1 `events_keep_days` in settings (default 90, `0` disables) with the same validation path as the other
      allowlisted settings.
- [x] 3.2 BLOCKED by #430: the guard matches `project:<key>` but the summary writes `project <key>`, so a summary row
      for a live project is pruned. Fix the shape before ticking.
      `internal/act/housekeep.go`: a retention step deleting in batches of 500, keeping every row whose item has a
      live dispatch or whose project has an unresolved queue row, writing one `housekeeping.retention` event per run
      with `scanned`, `deleted`, `kept_live`.
- [x] 3.3 Dry-run by default, like the other steps.
- [x] 3.4 Tests: window delete, live-work keep, dry run, disabled.

## 4. Docs and gate

- [x] 4.1 `docs/fleet-console.md`: a "Is it working?" section — what `/api/ready` checks, what the header colours
      mean, and how to read the retention event.
- [x] 4.2 `go build ./... && go vet ./... && go test ./...` green except the two known environment failures.
- [x] 4.3 `openspec validate --all --strict` green.

## Out of scope, filed separately

- Reviewer seat hard-coded (`act/review.go:53,130`, #384 closed without a merge) — three-line PR with a project key
  and a fallback.
- Operator rows with a deadline the board can show — needs the owner's numbers per purpose.
- Local branch hygiene beyond worktrees.
