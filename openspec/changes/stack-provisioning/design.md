# Design — stack provisioning and packaging

## 1. What "the stack" is

The app needs six classes of thing to exist before it is useful:

| Class | Members | Who can install it |
|---|---|---|
| Runtime | Node 24 (rig requires 22/24), Go 1.25 (build only) | script |
| Rig | `@openrig/cli` at the pinned version, daemon, kernel, spec library | script + `rig setup` |
| Harness | `omp` (+ optionally `claude`), one signed-in account | human for the login |
| Forge | `gh`, `glab`, one authenticated host each | human for the token |
| Search/tooling | `serena` (uv), `codegraph`, `rtk`, `gopls`, `jq`, `sqlite3`, `tmux`, `git` | script |
| The app | `fleet-console` binary, unit, env file, templates | script |

The script installs in that order because each later step reads a fact from an earlier one: `rig setup` writes the
harness trust files, seat launch reads the provider key, the app's compat probe reads the rig.

## 2. Shape of `install.sh`

- `set -Eeuo pipefail`, one action per step, every step prints `step N: <what>` and its exit code.
- **Dry-run is the default.** `--apply` writes. `--profile fleet|app`, `--dry-run`/`--apply`, `--verify`,
  `--upgrade`, `--lock <path>`.
- Every external install goes through the vendor's own installer or package, checksum-verified where a checksum
  exists; nothing is built from source on the target box.
- Every config write is a **marker block** (`# >>> fleet-console stack >>>`) or a `.bak-$(date)` copy. The script
  never rewrites `~/.claude.json`, `~/.tmux.conf` or `~/.gitconfig` wholesale.
- A step that cannot be automated stops with the exact command and an exit code of `3` (`needs-human`), which
  `--verify` treats as "resume from here", not as a failure.
- Logs: `~/.local/state/fleet-stack/install-<timestamp>.log`, plus a stable `latest.log` symlink.

## 3. `stack.lock`

A flat `key = value` file, one section per class: `node = 24.x`, `openrig = 0.7.0`, `go = 1.25.x`, `gh = <floor>`,
`glab = <floor>`, `rtk = <version>`, `codegraph = <version>`, `serena = <version>`, `fleet_console = <version>+sha256`.
The script installs the pinned version and **fails** when the installed binary answers a different version, rather
than silently continuing on whatever the distro had.

The lock is also the answer to "which rig will this app meet on a new box": the rig floor in `runtime-compat` must be
satisfied by the pinned OpenRig version, and a test asserts the two agree.

## 4. Human steps, named

The script stops at exactly four places, because these cannot be automated without holding a secret:

1. `claude` login (or `omp` provider key) — the harness prints its own flow.
2. `gh auth login` — device flow.
3. `glab auth login` — token entry.
4. Seat credentials — the script prints the exact path it will write
   (`~/.openrig/state/omp/<seat>/agent/config.yml`) and the key it expects in the environment; it never echoes the
   key, never writes it to the log, and seeds it only when the variable is present.

Each stop prints one copy-pasteable command and the sentence "then run `./install.sh --verify`".

## 5. Verify as the gate

`--verify` is the only definition of success:

- every tool answers its pinned version;
- `rig doctor` exits with exactly the two expected findings (Codex not installed, optional cmux) and no others;
- `rig daemon status` says running; `rig ps --nodes` answers;
- one real forge read (`gh api user`, `glab api user`) succeeds;
- `fleet-console --compat` exits 0 and prints the rig version, the floor and the two parsed shapes;
- `curl -fsS 127.0.0.1:8787/api/health` returns 200;
- the systemd user units are `active` and `enabled`, and linger is on.

A failed check prints the command to re-run, never "done".

## 6. Packaging

- **Binary**: `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"` with `go:embed dist` and `_ "time/tzdata"`.
  `fleet-console --version --json` prints version, commit, rig floor and build time.
- **Host tools are declared, not bundled**: `rig`, `gh`/`glab`, `git`, `tmux` must be on `PATH`. The binary checks
  them at boot and names the missing one.
- **Image (optional)**: multi-stage build producing an image with the binary, `gh`, `glab`, `rig`, `git`, `tmux`.
  It is only useful when it can see the host's `~/.openrig` and tmux socket, or when the whole fleet runs inside it.
  The `Dockerfile` comment says which mounts are required and that the default fleet-box deployment is the bare
  binary under systemd.
- **Upgrade**: `install.sh --upgrade` stops the unit, copies the binary to `<binary>.new`, runs `--migrate` on a
  backed-up DB (`cp app.db app.db.bak-<date>`), swaps the binary, starts the unit, then `--verify`. A failed verify
  restores the previous binary and leaves the DB backup in place.

## 7. Risks

| Risk | Mitigation |
|---|---|
| A new box gets a rig newer than the app's floor and the act loop refuses | the lock pins OpenRig and `runtime-compat` names the floor; verify proves the pair before anyone dispatches |
| The script silently "succeeds" on a half-installed box | `--verify` is separate and is the only success signal; every step prints its exit code |
| Two boxes diverge | one `stack.lock` and one `docs/my-stack.md`; a second box is brought up by this script, not by hand |
| A Docker deployment that cannot see the host's seats | the image is documented as requiring the host's rig state; the fleet box uses the binary |
