# Installing the fleet-console stack

`install.sh` takes an Ubuntu or Debian box to a running stack. Dry-run is the
default; `--verify` is the only definition of success.

```sh
./install.sh                 # dry-run: prints pins and steps, writes nothing
./install.sh --apply         # fleet profile: rig, harness, forge, tooling, app
./install.sh --apply --profile app   # app only: binary, unit, verify
./install.sh --verify        # resume after the human steps
```

Every version comes from [`stack.lock`](../stack.lock). The script installs the
pinned version and fails when the installed binary answers a different one.
The app artifact is `fleet-console-0.1.0-linux-amd64.tar.gz` from `make
release`, checked against both the tarball sha256 and the binary sha256 in the
lock. Publish that asset at the `fleet_console_url` release, or pass
`--app-tarball PATH` (or `--app-binary PATH` for a development build).
`x` is a wildcard (`24.x`), everything else must match exactly. The app pin is
the release tarball produced by `make release`, with its sha256.

## Profiles

| Profile | Steps |
|---|---|
| `fleet` | PATH, packages (Node 24, Go 1.25, apt tools), OpenRig, `rig setup`, tooling (serena, gopls, codegraph, rtk), the four human stops, the app, user units |
| `app` | the app (binary, env file, unit), user units, verify |

`app` is for a box that already runs a rig; it skips rig, harness and tooling.
It refuses a step whose prerequisite is missing.

## The four human stops

The script never holds a credential. Each of these prints one copy-pasteable
command and exits `3`; run `./install.sh --verify` afterwards.

1. Harness login — open `omp`, or import credentials with `omp auth import`.
2. `gh auth login` — device flow. The scopes are not optional: a classic token needs `repo` (plus `workflow` if
   builders touch `.github/workflows`); a fine-grained token needs Contents, Pull requests and Issues at write,
   because a merge is filed under Contents and nothing else merges — see
   [`forge-token.md`](forge-token.md) for the per-operation table.
3. `glab auth login` — token entry. The `api` scope, and Developer or better on the project.
4. Seat provider key — export `OMP_PROVIDER_KEY` in the environment (or write
   `~/.openrig/state/omp/<seat>/agent/config.yml`). The value is never printed
   and never written to the log.

No token-shaped value (`sk-…`, `ghp_…`, anything `*api_key*`) is written to
`~/.local/state/fleet-stack/latest.log`.

Configuration is only ever edited through a marker block
(`# >>> fleet-console stack >>>` … `# <<< fleet-console stack <<<`) or a
`.bak-<date>` copy. `~/.claude.json`, `~/.tmux.conf`, `~/.gitconfig` and any
existing rig spec under `~/.openrig/specs/rigs/` are left untouched.

## What verify proves

- every pinned tool answers the locked version (the profile's set);
- `rig doctor` has no unexpected findings (Codex and cmux are expected; the
  other harnesses' login findings are accepted because the fleet's harness is
  pinned by `stack.lock`);
- the rig daemon answers;
- one real forge read succeeds (`gh api user` or `glab api user`). A read never proves a write: no scope check
  sees a missing merge permission, so the first merge on a new box is the real test —
  [`forge-token.md`](forge-token.md) has the per-operation permission table and the symptom→cause list;
- `fleet-console --compat` exits 0 with the parsed queue and seat shapes;
- `GET http://127.0.0.1:8787/api/health` is 200;
- the user units are `active` and `enabled`, and linger is on;
- the env file is mode 600 and the installed binary's version matches the lock.

A failure prints the command that re-runs it and the script exits non-zero.
`--apply` on an installed box reports every step `already satisfied` and writes
nothing.

## Upgrade

```sh
./install.sh --upgrade --app-tarball fleet-console-0.1.0-linux-amd64.tar.gz
```

The upgrade stops `fleet-console`, copies the database to
`fleet-console.db.bak-<timestamp>`, runs `--migrate` with the new binary, swaps
the binary, starts the unit and checks `/api/health`. If migration or the
health check fails, the previous binary is put back, the unit is restarted, and
the log names the database backup.

## The Docker image is the exception

The default deployment is the bare binary under systemd. The unit's restart policy is what
makes a contained tick panic survivable — see *What `tick: panicked:` means* in
`docs/fleet-console.md`. `Dockerfile` builds an
image that includes `gh`, `glab`, `rig`, `git` and `tmux` for boxes that prefer
containers, but a container can only drive a host rig when it sees that host's
state. The Dockerfile header lists the required mounts (`~/.openrig`, the tmux
socket, `~/.ssh`, forge configs, the state directory). Without them the binary's
startup check reports the rig as unreachable.

## Host tools the binary declares

`fleet-console` checks `rig`, a forge CLI (`gh` or `glab`), `git` and `tmux` at
startup. A missing one is named and the process exits before serving; nothing is
dispatched.
