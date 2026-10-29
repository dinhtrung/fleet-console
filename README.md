# fleet-console

One control plane for agent fleets across **many projects, many repos, many rigs** — a Go binary with SQLite, a
REST + SSE API and a dense web board.

**Status: service skeleton plus the board.** This repository holds the specification of v1
(`openspec/changes/fleet-console-v1/`), the stack notes it grew out of (`docs/my-stack.md`), the web scaffold it
will be built on, inherited from [admin-panel](https://github.com/dinhtrung/admin-panel), and the Go service:
Fiber API, SQLite store, rig compat probe, the mirror and decide loops and the board. The act loop is not wired
into the server yet.

New to the project? [`docs/quickstart.md`](docs/quickstart.md) walks a bare Ubuntu box from prerequisites to a
serving board.

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

## Usage

Every command takes `--config FILE` (or `FLEET_CONSOLE_CONFIG`); the settings
resolve to flag > env > file > default. `--addr` and `--db` override the file
for one run. `--json` is only valid with `--version`.

| Command | What it does | One line you should see |
|---|---|---|
| `fleet-console --version` | build info and the rig floor, then exit | `fleet-console 0.1.0 (17a4dbe) built 2026-10-29T00:28:28Z; rig floor 0.7.0` |
| `fleet-console --version --json` | the same as JSON | `"version": "0.1.0",` |
| `fleet-console --config FILE --compat` | probe `rig`, print the report, exit non-zero below the floor | `"ok": true,` |
| `fleet-console --config FILE --print-config` | print the effective settings, token redacted, with each value's source | `token: <redacted>` |
| `fleet-console --config FILE --apply-config` | create or refresh the registry and exit; projects absent from the file are disabled; `--sync-onhold` copies `onhold` too | `created my-project` (then `unchanged my-project`; a removed project prints `disabled my-project`) |
| `fleet-console --config FILE --prune` | delete projects absent from the file and exit; refused while one has a live dispatch | `pruned my-project` |
| `fleet-console --config FILE --migrate` | open the database, apply migrations and exit | `database migrated` |
| `fleet-console --config FILE --tick` | one mirror + decide pass, printing the dry-run plan | `"status": "nothing",` (or `"intents"` with a dispatch) |
| `fleet-console --config FILE` | serve the API and the board | `fleet-console: listening on 127.0.0.1:8787` |
| `fleet-console --config FILE --import-dispatch-state FILE --project-key KEY` | import the old dispatcher's state and exit | `imported 1 dispatch row(s) into my-project` |

`--apply-config` never deletes a project that left the file: it sets
`enabled = 0` (`disabled <key>`) and keeps every row, so its history stays
readable and re-adding the block re-enables the same project id. It never resumes
a project that was paused in the database unless `--sync-onhold` is passed.
`--prune` is the only path that deletes a project; it refuses with the dispatch id
while a live dispatch exists. A server started with a config applies the same
registry at boot: present projects are enabled, absent ones disabled.

A server started with `--apply` (or `act: true` in the config) takes an
exclusive flock on `<database>.lock` for its lifetime. A second acting instance
exits with status **3** and `another instance owns <database>` before it opens
the database or starts the act loop. A killed holder leaves the lock file but
not the lock: flock dies with the process, so startup never waits on a stale
file.

## Configuration

Settings come from a strict `config.yaml`, flags and environment in that order:
**flag > env > file > default**. The file is found at `--config`, then
`FLEET_CONSOLE_CONFIG`, `$XDG_CONFIG_HOME/fleet-console/config.yaml`,
`~/.config/fleet-console/config.yaml`, `./fleet-console.yaml`; with no file the
built-in defaults preserve the previous flags-only behaviour. Unknown keys,
duplicate project keys, an unknown `forge` and an empty `repo` are errors that
name the file and line.

### Settings

| Key | Default | Env | Flag | Meaning |
|---|---|---|---|---|
| `listen` | `127.0.0.1:8787` | `FLEET_CONSOLE_ADDR` | `--addr` | API and board bind address; loopback by default |
| `database` | `fleet-console.db` | `FLEET_CONSOLE_DB` | `--db` | SQLite path |
| `token` | none | `FLEET_CONSOLE_TOKEN` | — | bearer token for the API and the board; required to serve |
| `token_file` | none | — | — | file holding the token; **must be mode `0600`** |
| `rig_binary` | `rig` | `FLEET_CONSOLE_RIG_BIN` | — | rig CLI the app executes |
| `gh_binary` | `gh` | `FLEET_CONSOLE_GH_BIN` | — | GitHub CLI for `forge: github` |
| `glab_binary` | `glab` | `FLEET_CONSOLE_GLAB_BIN` | — | GitLab CLI for `forge: gitlab` |
| `tick_interval` | `15s` | `FLEET_CONSOLE_TICK_INTERVAL` | `--tick-interval` | mirror pass interval |
| `project_timeout` | `base: 30s`, `per_item: 2s`, `ceiling: 2m` | — | — | one project's read deadline: `base + per_item*items`, capped at `ceiling`; per-item forge reads run 8-wide |
| `act` | `false` | — | `--apply` | apply the decide plan through the act loop: worktree, queue row, verdict, merge. Off means the board is read-only |
| `projects` | `[]` | — | — | the registry (file only) |

Token precedence is `FLEET_CONSOLE_TOKEN` > inline `token` > `token_file`;
an inline token is accepted but warns, because the file is easier to keep at
`0600`. A `token_file` with any other mode refuses to load. Relative paths in
the file resolve against the file's directory and `~` expands, so the file can
live in `~/.config/fleet-console/` and point at `~/src/...`.

A project entry takes `key`, `name`, `forge` (`github`/`gitlab`), `repo`
(`owner/name`), `default_branch` (default `main`), `rigs`, `rig_template`
(default `default`), `concurrency_cap`, `gate_cmd`, `labels`, `onhold`, and
`paths.main` / `paths.worktrees`. `rigs` is the ordered list of rigs for this
project; `rig: name` is the one-element form, and using both in one file is an
error. At least one rig is required, and a rig used by one project may not be
listed by another. Each rig serves only this project: the
dispatcher fills free builders across the list in order under
`concurrency_cap`, and a review goes to `dev-review@<rig>` for the rig that
built the item (the brief names it). A configured rig with no running seats is
named by `--tick` (a `rig: …` line on stderr and `rig_warnings` in its JSON) and
logged once by the server, and is not dispatched to; a failed rig read is
reported as `rig_stale` and names no rig, because a read that did not answer
cannot say a rig is empty. `paths.main` is the working checkout the
mirror reads; a missing path is a warning, not an error.

### config.yaml

A complete, runnable example (only `key`, `forge`, `repo` and a rig are strictly
required; adjust the paths and the repository):

```yaml
# ~/.config/fleet-console/config.yaml
listen: 127.0.0.1:8787
database: ~/.local/state/fleet-console/fleet-console.db
token_file: ~/.config/fleet-console/token
rig_binary: rig
gh_binary: gh
glab_binary: glab
tick_interval: 15s
# one project's forge/git read deadline; scaled by the cached item count.
project_timeout:
  base: 30s
  per_item: 2s
  ceiling: 2m
# false: the board only reads and plans. true (or --apply) executes the plan.
act: false

projects:
  - key: my-project
    name: My project
    forge: github
    repo: my-org/my-project
    default_branch: main
    rigs: [my-project]
    rig_template: default
    concurrency_cap: 1
    gate_cmd: scripts/gate.sh
    labels:
      area: backend
    onhold: false
    paths:
      main: ~/src/my-project
      worktrees: ~/src/worktrees
```

`onhold` is the project pause: the config never resets it on a normal apply,
and `--apply-config --sync-onhold` is the one path that copies it into the
database. `--apply-config` is idempotent: the second run prints `unchanged`.
A project removed from `projects:` is disabled (paused on the board) rather than
forgotten; `--prune` deletes it only once it has no live dispatch.

### Two-phase dispatch

Dispatch writes the app's dispatch row first (`state = pending`), creates the rig
queue row, then stamps `qitem_id` and marks it active. Boot and every acting tick
resolve a pending row: one queue row matching the exact brief summary (or the
branch in its body) is stamped; no match is marked `abandoned`; more than one is
abandoned and raises one queue decision for a human. A crash between the queue
row and the stamp therefore never creates a second row, and an abandoned attempt
does not block the next dispatch.

### Where state lives

Everything is in the SQLite file at `database`; the database never stores the
token.

- `project` / `repo` — the registry, refreshed from `config.yaml`.
- `item` / `dispatch` / `snapshot` / `mirror_meta` — the read-only forge and rig
  mirror, refreshed every `tick_interval` and by `--tick`.
- `verdict` / `event` — a review verdict (sha, decision, source) and the audit
  trail of what the app did.
- The rig owns its own queue, decisions and leases; the app reads them through
  `rig queue` and never rewrites them. The forge owns the issue, labels and pull
  request; the app records review verdicts and events, and the act loop (not
  wired into the server yet) is the only component that sets the
  `status::ongoing` marker.

Back up the database with SQLite itself (safe while serving) or copy it while
the service is stopped:

```bash
sqlite3 ~/.local/state/fleet-console/fleet-console.db ".backup '/tmp/fleet-console-backup.db'"
```

## Gates

```bash
openspec validate --all --strict      # the contract is valid; every requirement has a scenario
npm run typecheck && npm run lint     # the inherited scaffold still builds
```
