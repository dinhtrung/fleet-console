# Quickstart: a bare Ubuntu box to a serving board

This walks one Ubuntu 24.04 host from nothing to `fleet-console` serving the board
on loopback, with one project registered and its rig visible. Every command is
copy-pasteable; each step says what success looks like. Replace `my-org`,
`my-project` and the paths with yours.

The board binds loopback only. For a remote box, reach it through an SSH tunnel
(step 7); never expose port 8787 to the network.

## 1. Prerequisites

```bash
rig --version           # >= 0.7.0 (the compat floor)
gh auth status          # logged in to the forge that hosts the project
git --version
tmux -V
curl --version
sqlite3 --version       # for the backup in step 9
```

Building from source also needs Go 1.25 and Node 24; the release tarball needs
neither. `rig` must be the same daemon that owns the project's queue: the app
runs `rig queue`/`rig status`, not its own queue.

**Success:** `rig --version` prints at least `0.7.0`, and `gh auth status`
reports a logged-in account.

## 2. Install

Take the latest release that includes the configuration commands
(`--print-config`); if the newest release predates them, use the source build
at the end of this step. The asset name is
`fleet-console-<version>-linux-<arch>.tar.gz`; the `.sha256` beside it is the
same line `sha256sum -c` checks. Use `arm64` on an ARM box.

```bash
TAG=$(gh release view --repo dinhtrung/fleet-console --json tagName -q .tagName)
VERSION=${TAG#v}
BASE=https://github.com/dinhtrung/fleet-console/releases/download/$TAG
mkdir -p "$HOME/fleet-console-release" "$HOME/src"
cd "$HOME/fleet-console-release"
curl -fLO "$BASE/fleet-console-$VERSION-linux-amd64.tar.gz"
curl -fLO "$BASE/fleet-console-$VERSION-linux-amd64.tar.gz.sha256"
sha256sum -c "fleet-console-$VERSION-linux-amd64.tar.gz.sha256"
tar -xzf "fleet-console-$VERSION-linux-amd64.tar.gz" fleet-console

mkdir -p "$HOME/.local/bin" "$HOME/.config/systemd/user"
install -m 0755 fleet-console "$HOME/.local/bin/fleet-console"

git clone https://github.com/dinhtrung/fleet-console.git "$HOME/src/fleet-console"
install -m 0644 "$HOME/src/fleet-console/deploy/fleet-console.service" \
  "$HOME/.config/systemd/user/fleet-console.service"
systemctl --user daemon-reload
```

The tarball carries only the binary; the repository carries the systemd unit.
`./install.sh --apply --profile app` does the same binary + env file + unit in
one step when `stack.lock` already pins the release you downloaded — run
`./install.sh --verify` afterwards and expect exit 0.

Building from source instead (Go 1.25, Node 24) produces the same binary:

```bash
cd "$HOME/src/fleet-console" && make build
install -m 0755 fleet-console "$HOME/.local/bin/fleet-console"
```

**Success:** `fleet-console --version` prints a version line, for example
`fleet-console 0.1.0 (17a4dbe) built 2026-10-29T00:28:28Z; rig floor 0.7.0`,
and `sha256sum -c` printed `<tarball>: OK`.

## 3. Token

The API and the board need a bearer token. Keep it in two places: a `0600` file
the config reads (`token_file`), and a `0600` `EnvironmentFile` the systemd unit
reads. The environment wins over the file, so the service can move the token
without editing the config.

```bash
mkdir -p "$HOME/.config/fleet-console" "$HOME/.local/state/fleet-console"
TOKEN=$(openssl rand -hex 32)
(umask 077; printf '%s\n' "$TOKEN" > "$HOME/.config/fleet-console/token")
(umask 077; printf 'FLEET_CONSOLE_TOKEN=%s\n' "$TOKEN" > "$HOME/.config/fleet-console/fleet-console.env")
chmod 600 "$HOME/.config/fleet-console/token" "$HOME/.config/fleet-console/fleet-console.env"
unset TOKEN
```

Why `0600`: the file is a credential; the config refuses any other mode and
names the file. Do not paste the token into a shell history or a repository.

**Success:** `stat -c '%a %n' "$HOME"/.config/fleet-console/*` prints `600` for
both files.

### Telegram, optional

Two more environment variables, in the same `EnvironmentFile`:

```bash
(umask 077; printf 'TELEGRAM_BOT_TOKEN=%s\nTELEGRAM_CHAT_ID=%s\n' "<token>" "<chat id>" \
  >> "$HOME/.config/fleet-console/fleet-console.env")
```

`TELEGRAM_BOT_TOKEN` is the bot's token and `TELEGRAM_CHAT_ID` the conversation
that receives decision escalations and progress reports.

**Unset means off.** With either missing, the service prints one line at boot —
`notify: TELEGRAM_BOT_TOKEN/TELEGRAM_CHAT_ID not set; completion notices are off`
— and sends nothing; the board and the act loop are unaffected. That is a
configuration, not a failure, so a box without a phone costs nothing.

To try it without waiting for a merge:

```bash
fleet-console --config "$CONFIG" --notify-report --notify-project my-project
```

That renders the report from the store and the rig roster — no mirror, no
dispatch — and exits `0` delivered (or already current), `1` when the transport
fails, and `3` when the two variables are unset. `notify_interval: 30m` in the
config adds an optional digest at most that often, and only when the rendered
state changed; the default `0` is off.


**The board has no sign-in screen.** It is served on a loopback listener, so a
request from `127.0.0.1` is granted the board session when the page loads: open
`http://127.0.0.1:8787/` and the board is there. Every other caller still needs
the token: the `Authorization: Bearer` header on `/api`, `POST /api/session`, or the
`/login` form — which is what protects the surface if the listen address is ever
changed to anything other than loopback.

## 4. Config

The minimal file for the first project. `~` expands and relative paths resolve
against this file's directory; `paths.main` is the checkout the mirror reads.

```bash
cat > "$HOME/.config/fleet-console/config.yaml" <<'YAML'
listen: 127.0.0.1:8787
database: ~/.local/state/fleet-console/fleet-console.db
token_file: ~/.config/fleet-console/token
rig_binary: rig
gh_binary: gh
glab_binary: glab
tick_interval: 15s
project_timeout:
  base: 30s
  per_item: 2s
  ceiling: 2m
act: false   # true (or --apply) executes the plan: worktree, row, verdict, merge

projects:
  - key: my-project
    name: My project
    forge: github
    repo: my-org/my-project
    default_branch: main
    rigs: [my-project]
    concurrency_cap: 1
    paths:
      main: ~/src/my-project
      worktrees: ~/src/worktrees
YAML
chmod 600 "$HOME/.config/fleet-console/config.yaml"
```

The full schema and every default are in the README's Configuration section.

**Success:** `fleet-console --config "$HOME/.config/fleet-console/config.yaml" --print-config`
prints YAML beginning `config: /home/<user>/.config/fleet-console/config.yaml`,
with `token: <redacted>` and a `sources:` block; it never prints the token.

## 5. Seed and check

```bash
CONFIG="$HOME/.config/fleet-console/config.yaml"
fleet-console --config "$CONFIG" --apply-config      # created my-project
fleet-console --config "$CONFIG" --apply-config      # unchanged my-project
fleet-console --config "$CONFIG" --compat            # JSON report, exit 0
fleet-console --config "$CONFIG" --migrate           # database migrated
```

`--apply-config` creates or refreshes the registry, prints one
`created|updated|unchanged <key>` line per project, and never deletes a project
that left the file. It runs again at boot when the service is started with
`--config`.

**Success:** the four commands exit 0; the second one prints `unchanged my-project`
and `--compat` shows `"ok": true`.

## 6. Run it

Foreground first, so failures are visible:

```bash
fleet-console --config "$CONFIG"
```

Move it to the user service once it serves. The unit installed in step 2 reads
`FLEET_CONSOLE_TOKEN` from the env file and sets `--addr`/`--db`; this drop-in
makes the config file authoritative so boot also applies the registry:

```bash
mkdir -p "$HOME/.config/systemd/user/fleet-console.service.d"
cat > "$HOME/.config/systemd/user/fleet-console.service.d/config.conf" <<'EOF'
[Service]
ExecStart=
ExecStart=%h/.local/bin/fleet-console --config %h/.config/fleet-console/config.yaml
EOF
systemctl --user daemon-reload
systemctl --user enable --now fleet-console
systemctl --user restart fleet-console       # after editing the unit or the config
loginctl enable-linger "$USER"               # start at boot without a login session
journalctl --user -u fleet-console -n 5 --no-pager
```

The unit has `Restart=on-failure` and an `EnvironmentFile`; the config's
`database` must be the path the service can write (the example uses
`~/.local/state/fleet-console/`). Without the drop-in the service still serves
the applied registry from its `--db`, but it does not re-apply the file at boot.

**Success:** `systemctl --user is-active fleet-console` prints `active`, and the
journal shows `listening on 127.0.0.1:8787` after the compat line.

## 7. Reach the board

From your laptop, tunnel the loopback port; the port stays closed on the box's
network interface:

```bash
ssh -N -L 8787:127.0.0.1:8787 you@box
```

Open <http://127.0.0.1:8787>: over the tunnel the request still arrives from
loopback, so the board opens with no sign-in. The
curl check uses the same token:

```bash
curl -sS -H "Authorization: Bearer $(cat "$HOME/.config/fleet-console/token")" \
  http://127.0.0.1:8787/api/board | head -c 300
```

The board table has six columns: **#** (the issue number), **Task**, **State**,
**Seat**, **Age**, and **Why it is not moving**. The state magnets mean:

| Code | State | Meaning |
|---|---|---|
| BLD | Building | a dispatch is open; the seat is working |
| REV | Review | a verdict exists and the merge is pending, or it merged |
| WAI | Waiting | blocked by another open issue, a dispatch ended, or no free builder |
| PRK | Parked | a change request is open with no verdict |
| QUE | Queued | eligible, no dispatch yet |
| BLK | Blocked | parent closed, compat below floor, or forge error |
| PSE | Paused | the project is `onhold` |

**Success:** the board shows the project panel from step 5 and `/api/board`
returns `"projects":[{...}]`.

## 8. First ticket

Create a normal open issue; the dispatcher sets the `status::ongoing` marker
itself when it dispatches. Do not add that marker by hand.

```bash
gh issue create --repo my-org/my-project \
  --title "First task" --body "A task the fleet may pick up."
fleet-console --config "$CONFIG" --tick
```

`--tick` is one read-only mirror + decide pass: it re-reads the forge and the
rig, updates the mirror tables and prints the plan. A fresh ticket with no
dispatch shows as **Queued**; a live dispatch shows **Building** with the seat in
the Seat column, and a row another queue item already owns shows `live_row` in
the plan. This build's server mirrors and plans but does not open dispatches —
the act loop that does is not wired into the server yet — so the state stays
Queued until a dispatch row exists (for example after
`--import-dispatch-state`). The rig's live seats are on the seats screen and at
`/api/seats`.

**Success:** the issue URL is printed, `--tick` exits 0 with a JSON plan naming
the project, and the board lists the new issue with its state and reason.

## 9. Pause, stop, back up

Pause the project by syncing the file's pause into the database:

```bash
# set "onhold: true" under the project in $CONFIG, then:
fleet-console --config "$CONFIG" --apply-config --sync-onhold   # updated my-project
```

The board shows the project as paused and its rows as **PSE**. Set
`onhold: false` and run the same command to resume. A normal apply never flips
`onhold`, so a pause survives restarts and config edits.

Stop and back up:

```bash
systemctl --user stop fleet-console
systemctl --user disable fleet-console        # optional: do not start at login
sqlite3 "$HOME/.local/state/fleet-console/fleet-console.db" \
  ".backup '$HOME/fleet-console-backup.db'"
```

`.backup` is safe while serving; a plain `cp` is only safe while stopped.

**Success:** `systemctl --user is-active fleet-console` prints `inactive` after
the stop, and the backup file exists and opens (`sqlite3 <backup> 'select count(*) from project;'`).

## 10. Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `compat probe failed: ...` or `"ok": false` | `rig` is older than 0.7.0 or not working | `rig --version`; upgrade rig to at least 0.7.0 |
| `missing host tools on PATH: rig (rig); the act loop stays off` | the rig CLI is not on `PATH` | install rig, or set `rig_binary` to its absolute path |
| `HTTP/1.1 401 Unauthorized` | no or wrong bearer token | use the token from step 3; `FLEET_CONSOLE_TOKEN` beats `token_file` |
| Board shows “No projects”, or `/api/board` returns `"projects":[]` | the config has no projects or it was never applied | run `--apply-config`; check `database` is the same file the service uses |
| `failed to listen: ... bind: address already in use` | another process owns the port | stop it, or change `listen`/`--addr` |
| `token_file ... has mode 0644; must be 0600` | wrong permissions | `chmod 600` the file |
| `field ... not found in type config.File` (with `line N`) | unknown key or typo | fix the named line; the schema is strict |
| `--tick` hangs or exits with `rig_error` / `context canceled` | the rig CLI is not answering | `rig status` and `rig queue list`; restart the rig daemon, then run `--tick` again |
| `missing host tools on PATH` names git or tmux | the base tools are absent | `sudo apt install git tmux` |

When a check could not run, say so: `--compat` is the authority on the rig, and
`--print-config` is the authority on what a run will use.
