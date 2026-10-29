# fleet-console

One control plane for agent fleets across **many projects, many repos, many rigs** — a Go binary with SQLite, a
REST + SSE API and a dense web board.

**Status: design plus the service skeleton.** This repository holds the specification of v1
(`openspec/changes/fleet-console-v1/`), the stack notes it grew out of (`docs/my-stack.md`), the web scaffold it
will be built on, inherited from [admin-panel](https://github.com/dinhtrung/admin-panel), and the Go service
skeleton: Fiber API, SQLite store and rig compat probe (tasks 1.1–1.3, 1.6). The loops and the board are not
implemented yet.

**License:** MIT.

## The problem

A single-repo dispatcher works until the second project exists. Then there is no cross-project view, state lives in
a JSON file with no transactions and no audit, and "the fleet is idle" needs four separate reads to discover that
the real answer is *a review verdict is parked, waiting for a human*. Both failures are silent, and both waste hours.

## What v1 specifies

- **A registry**: project → 1..n repos, one rig, config without secrets, a per-project concurrency cap.
- **Durable state**: tasks, dispatches, leases, decisions and events in SQLite — not a JSON file.
- **Three loops, one writer**: mirror (read), decide (pure, replayable), act (audited, serialized).
- **Decisions as records**: every obligation waiting on a human, with an owner, an SLA and one-click actions.
- **A deterministic merge watchdog**: merge only on four facts — an `ACCEPT <sha>`, that sha still at the head, a
  green head pipeline, a mergeable MR that `Closes #<id>`.
- **A board whose main job** is to show *why* nothing is moving, per task, and to clear it.

Read the design: [`openspec/changes/fleet-console-v1/design.md`](openspec/changes/fleet-console-v1/design.md).
The manual workflow it productises: [`docs/my-stack.md`](docs/my-stack.md) §3.

## Layout

| Path | What |
|---|---|
| `openspec/changes/fleet-console-v1/` | the contract: proposal, design, phased tasks, four capability deltas |
| `cmd/`, `internal/` | the Go service skeleton: Fiber API, SQLite store, rig compat probe |
| `docs/my-stack.md` | the stack and the workflow this app automates (project details removed) |
| `src/`, `DESIGN.md`, `PRODUCT.md` | the web scaffold inherited from admin-panel; the board will replace its screens |

## Configuration

Settings come from a strict `config.yaml`, flags and environment in that order:
**flag > env > file > default**. The file is found at `--config`, then
`FLEET_CONSOLE_CONFIG`, `$XDG_CONFIG_HOME/fleet-console/config.yaml`,
`~/.config/fleet-console/config.yaml`, `./fleet-console.yaml`; with no file the
built-in defaults preserve the previous flags-only behaviour.

| Key | Env | Default |
|---|---|---|
| `listen` | `FLEET_CONSOLE_ADDR` | `127.0.0.1:8787` |
| `database` | `FLEET_CONSOLE_DB` | `fleet-console.db` |
| `token` / `token_file` | `FLEET_CONSOLE_TOKEN` | none (required to serve) |
| `rig_binary` | `FLEET_CONSOLE_RIG_BIN` | `rig` |
| `gh_binary` / `glab_binary` | `FLEET_CONSOLE_GH_BIN` / `FLEET_CONSOLE_GLAB_BIN` | `gh` / `glab` |
| `tick_interval` | `FLEET_CONSOLE_TICK_INTERVAL` | `15s` |

Relative paths in the file resolve against the file's directory; `~` expands.
`token_file` must be mode `0600`. An inline `token` warns: prefer the file.

`projects:` is the registry: `key`, `name`, `forge` (`github`/`gitlab`),
`repo` (`owner/name`), `default_branch`, `rig`, `rig_template`,
`concurrency_cap`, `gate_cmd`, `labels`, `onhold`, and `paths.main` /
`paths.worktrees`. `--apply-config` creates or refreshes every entry and prints
`created|updated|unchanged <key>`; the same apply runs at boot. It never deletes
a project, and `onhold` stays DB-owned unless `--sync-onhold` asks to copy it.

```bash
fleet-console --config testdata/config.yaml --apply-config
fleet-console --config testdata/config.yaml --print-config   # token redacted, values' sources
```

## Gates

```bash
openspec validate --all --strict      # the contract is valid; every requirement has a scenario
npm run typecheck && npm run lint     # the inherited scaffold still builds
```
