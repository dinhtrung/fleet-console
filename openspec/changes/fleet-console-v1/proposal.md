# Proposal — fleet-console v1

## Why

The automatic fleet in `~/my-stack.md` §3.8 works for one repo on one rig: `dispatcher.py` (stdlib +
`glab` + `rig`), a JSON state file, one timer, one log. Two things break when there is a second project:

- **There is no cross-project view.** "Why is the fleet idle?" needs `rig ps`, the tick log, `rig queue list` and
  the forge — four reads, and the answer ("a verdict is parked, waiting for me") is invisible for hours. It happened
  twice this week: #41 sat parked for two hours, and #42 for four, both reported as idle.
- **Everything is per-repo.** A second repo means a second copy of the script, timer, log and state file, and no
  place that knows which rig serves which project, or that a rig's builders are the shared capacity.

## What changes

One internal app — a Go binary with SQLite, a REST+SSE API and a web board — that keeps the loop's rules exactly and
adds the four things that make it run N projects: a **registry** (project → repos, rig, seats, gates, labels),
**durable state** (tasks, dispatches, leases, decisions, events instead of one JSON), a **decisions queue** (every
blocking obligation as a record with an owner and an SLA, answerable in one click), and a **deterministic merge
watchdog** (four facts, no judgement).

## In scope (v1)

- Registry for N projects, each with 1..n repos and one rig; concurrency capped per project.
- Mirror loop over forge + rig + git branches; board that names the reason a task is not moving.
- Decide loop implementing §3.8's ordering and blocking rules, dry-run by default, replayable offline.
- Act loop: dispatch, re-nudge, marker reconcile, epic close, row resolve/handoff, merge watchdog.
- Decisions queue with SLA and Telegram escalation; audit trail of every write.

## Out of scope

- Replacing `rig` or the forge; any code writing; model-driven ordering or "smart" prioritisation.
- Multi-tenant auth beyond a bearer token + read-only token; a hosted deployment.
- Cost dashboards (a report on demand instead).

## Risk and the mitigation

| Risk | Mitigation |
|---|---|
| Two writers during cutover double-dispatch | the console's project lease + a golden test that compares its plan with `dispatcher.py`'s every tick for a week |
| A mirror bug hides work ("no task ready" while one is ready) | every skip reason is a stored row, and the board shows it; a task with no reason is a test failure |
| Merging on stale facts | the watchdog re-reads all four facts in the act pass, never from the mirror |
| The app becomes a second source of truth for rig state | rig remains authoritative; the mirror is stamped with its sync time on every screen |

## Success

- One screen answers "what is the fleet doing, per project, and what waits for me" in under five seconds.
- A parked verdict is escalated before its SLA instead of being found hours later.
- Onboarding a second repo is a row in the registry, not a new script and timer.
- The dry-run plan for `app` matches the existing dispatcher's on every tick for a week before any write is enabled.
