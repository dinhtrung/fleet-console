# Proposal — stack provisioning and packaging

## Why

`fleet-console` drives tools it does not ship: the OpenRig CLI and daemon, a coding harness signed in per account, a
forge CLI with a token, serena/codegraph/rtk, tmux, Go. Today that stack exists because a person installed it and
wrote `docs/my-stack.md`; on a second box it is rebuilt from memory, in a different order, with different versions —
and the app then fails on a rig four releases newer or older than the one it was written against.

Two deliverables close that gap:

1. **`install.sh`** — one idempotent script that takes a bare Ubuntu box to a running stack, with a pinned
   `stack.lock`, two profiles, an explicit stop for the steps only a human can do, and a verify phase that decides
   success.
2. **One portable artifact** — the app as a single static binary (board embedded, SQLite pure Go, tzdata embedded)
   that can be copied to another fleet box, plus an optional image for boxes that prefer containers.

## What changes

- `stack.lock` names every tool and version the stack needs, and the script refuses to install a version it cannot
  verify was the pinned one.
- Profiles: `fleet` (rig + harness + app on a box that will run seats) and `app` (only the binary and its unit on a
  box that already has a rig).
- The script stops at non-automatable steps (harness login, forge token, provider key) and prints the exact command;
  `--verify` resumes after the human has done them.
- `verify` is the gate: versions against the lock, `rig doctor` with the two expected failures named, a real forge
  API read, a running daemon, `fleet-console --compat` green and `GET /api/health` 200.
- Packaging: `CGO_ENABLED=0` build with `go:embed` and `_ "time/tzdata"`; an optional multi-stage image that includes
  the host tools and says, in the same file, what it must mount to reach the host's rig.

## Capabilities

### New Capabilities

- `provisioning` — the install script, lock file, profiles, auth stop and verify gate.
- `packaging` — the single binary, its declared host dependencies, the optional image, and the in-place upgrade.

## Impact

- New files at the repository root: `install.sh`, `stack.lock`, `deploy/fleet-console.service`,
  `deploy/rig-daemon.service`, `Dockerfile`.
- No change to the product contract in `fleet-console-v1`; this change set only decides how the stack is installed
  and how the binary travels.
- The Docker path is documented as the exception, not the default: a container that cannot see the host's
  `~/.openrig` and tmux server cannot drive that host's seats.
