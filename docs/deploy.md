# Deploying the fleet-console gate

The merge gate is only fixed on a box that runs the new binary. This is the
runbook for updating an existing install from the checkout — the everyday path,
not the fresh-box one. A fresh box is [`install.sh`](../install.sh) (see
[`install.md`](install.md)); this page assumes the service, the config and the
database already exist.

## The layout

| What | Where |
|---|---|
| checkout | `~/develop/fleet-console` |
| binary the unit runs | `~/.local/bin/fleet-console` |
| config | `~/.config/fleet-console/config.yaml` |
| database | `~/.local/state/fleet-console/fleet-console.db` |
| user unit | `fleet-console.service` (systemd `--user`) |

The unit's `ExecStart` is
`%h/.local/bin/fleet-console --config %h/.config/fleet-console/config.yaml`, so
nothing in the unit changes when the binary does.

## 1. Build

```sh
cd ~/develop/fleet-console
git pull --ff-only
make build                      # rebuilds the web UI, then the Go binary
./fleet-console --version
```

`make build` runs `npm ci` and `npm run build` before the Go build, so the board
the binary serves is the one in the checkout. It writes `./fleet-console` in the
checkout; it does not touch `~/.local/bin`.

`--version` prints the build's provenance — the version, the short sha of the
commit, and the UTC time of the build:

```
fleet-console 0.1.0 (f41bbda) built 2026-11-13T14:57:41Z; rig floor 0.7.0
```

Check it: the sha should equal `git rev-parse --short HEAD`, and the build time
should be the one you just made. A version whose sha is older than `HEAD` is a
stale binary — the deploy did not build what you think it did. (`make build`
stamps `VERSION`, `0.1.0` by default; a bare `go build` prints `dev` and no
timestamp, and that is a binary you did not make for a deploy.)

## 2. Install and restart

Keep the running binary: it is the rollback.

```sh
install -m 0755 ~/.local/bin/fleet-console ~/.local/bin/fleet-console.prev
install -m 0755 ~/develop/fleet-console/fleet-console ~/.local/bin/fleet-console
~/.local/bin/fleet-console --version    # the new sha, before the restart
systemctl --user restart fleet-console.service
systemctl --user is-active fleet-console.service   # → active
```

## 3. Verify the parser

The gate's job is to read a verdict out of a reviewer's prose. Follow the tick
as it runs — its cadence is the project's `tick_interval` (60s in this box's
config):

```sh
journalctl --user -u fleet-console.service -f | rg 'act:'
```

A **wrapped verdict** — the reviewer's `ACCEPT <sha>` inside prose — is read as a
verdict, and the tick that read it says so in the per-project summary:

```
act: tsim dispatches=0 verdicts=1 merges=0 skipped=0
```

`verdicts=` counts notes captured as verdicts this tick; `merges=` counts what
the gate then merged. A **capture** is the fix; no merge may follow (a stale sha,
a red pipeline or an unreadable body still refuses, and `act-skip:` names why).

An **ambiguous** note is the other half: it raises a question instead of guessing,
and the question is a ledger row:

```sh
sqlite3 ~/.local/state/fleet-console/fleet-console.db \
  "SELECT id, at, subject, detail FROM event
     WHERE kind = 'decision.verdict_question'
     ORDER BY id DESC LIMIT 5;"
```

One row per (item, note, head). The `detail` carries the project, the item, the
change request, the note id, the head and the row id it created — the same facts
the board lists. The board shows a question while its queue row is unsettled and
drops it once the queue says the row is finished; how that row gets closed is the
tick's business, not this page's, and a question that outlives its answer is
visible as exactly that.

## 4. Verify the seat

A question is addressed to a seat, and the seat must exist:

```sh
rig status                        # kernel: 4 node(s)
rig ps --nodes --rig kernel       # names them: operator.agent, advisor.lead, …
```

The destination is a session name: the logical id's dot becomes a dash and the
rig is the suffix — `operator.agent` on `kernel` is `operator-agent@kernel`. A
question row for an `operator`-mode relay says so in its event detail
(`asked operator-agent@kernel`), and the row is readable end to end:

```sh
rig queue list -a --json | jq '.[] | select(.destination == "operator-agent@kernel")'
```

## 5. Roll back

One command, using the binary saved in step 2:

```sh
install -m 0755 ~/.local/bin/fleet-console.prev ~/.local/bin/fleet-console &&
  systemctl --user restart fleet-console.service
```

`~/.local/bin/fleet-console.prev` holds the build that was running before this
deploy, and `--version` prints its sha if you need to prove what it is.

**One caveat: the database is forward-only.** Migrations are applied in order and
a binary refuses a database newer than it supports
(`database schema version %d is newer than this binary supports`). Rolling back
across a migration therefore needs the database from before it — take a copy
before a deploy that ships one:

```sh
cp ~/.local/state/fleet-console/fleet-console.db \
   ~/.local/state/fleet-console/fleet-console.db.$(date +%Y%m%dT%H%M%S)
```

A deploy that only changes behaviour rolls back with the binary alone.
